package realtime

// Совместный редактор кода: документ Yjs (тексты «code» и «stdin», карта «meta»).
//
// Сервер не разбирает Yjs — он ретранслятор с журналом. Каждое бинарное обновление Yjs получает
// порядковый номер seq, сохраняется и рассылается остальным участникам; отправитель получает
// подтверждение (code.ack). Обновления Yjs коммутативны и идемпотентны, поэтому порядок доставки
// и повторы не важны, а одновременные правки сливаются без потери текста. Клиент применяет чужие
// обновления с отдельным origin и не отправляет их обратно — бесконечного обмена нет.
//
// Новому клиенту (и после переподключения) отправляется весь журнал (code.snapshot); клиент в ответ
// досылает изменения, которых нет на сервере (вычисляет разницу по state vector).
//
// Сжатие: когда журнал вырастает, сервер просит одного клиента прислать полное состояние,
// содержащее все обновления до seq=upTo (клиент получает сообщения строго по порядку seq, поэтому
// к моменту обработки запроса у него есть всё до upTo). Сервер заменяет этим состоянием префикс журнала.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"
)

const (
	maxCodeUpdateBytes = 256 << 10 // одно обновление (вставка большого фрагмента)
	maxCodeDocBytes    = 2 << 20   // весь журнал урока
	compactEntries     = 300
	compactBytes       = 256 << 10
	compactTimeout     = 30 * time.Second
)

type codeEntry struct {
	seq  int64
	data []byte
}

type codeState struct {
	entries []codeEntry
	bytes   int
	seq     int64
	dirty   bool
	// ожидаемое сжатие
	compactFrom *Client
	compactUpTo int64
	compactAt   time.Time
}

func (h *Hub) loadCode(ctx context.Context, lessonID string) codeState {
	var st codeState
	var ups [][]byte
	if err := h.db.QueryRow(ctx, `SELECT updates, seq FROM code_docs WHERE lesson_id=$1`, lessonID).Scan(&ups, &st.seq); err != nil {
		return codeState{}
	}
	for _, u := range ups {
		st.entries = append(st.entries, codeEntry{seq: st.seq, data: u})
		st.bytes += len(u)
	}
	return st
}

func (h *Hub) persistCode(r *room) {
	r.mu.Lock()
	if !r.code.dirty {
		r.mu.Unlock()
		return
	}
	ups := make([][]byte, len(r.code.entries))
	for i, e := range r.code.entries {
		ups[i] = e.data
	}
	seq := r.code.seq
	r.code.dirty = false
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.db.Exec(ctx, `INSERT INTO code_docs(lesson_id, updates, seq, updated_at) VALUES ($1,$2,$3, now())
		ON CONFLICT (lesson_id) DO UPDATE SET updates=$2, seq=$3, updated_at=now() WHERE code_docs.seq <= $3`, r.id, ups, seq)
	if err != nil {
		// Содержимое документа (код) в лог не пишем.
		slog.Error("persist code", "lesson", r.id, "err", err)
		r.mu.Lock()
		r.code.dirty = true
		r.mu.Unlock()
	}
}

func encodeUpdates(es []codeEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = base64.StdEncoding.EncodeToString(e.data)
	}
	return out
}

func (c *Client) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		c.enqueue(b)
	}
}

func (h *Hub) codeSync(r *room, c *Client) {
	r.mu.Lock()
	msg := map[string]any{"type": "code.snapshot", "updates": encodeUpdates(r.code.entries), "seq": r.code.seq}
	r.mu.Unlock()
	c.sendJSON(msg)
}

func (h *Hub) codeUpdate(r *room, from *Client, update, cid string) {
	data, err := base64.StdEncoding.DecodeString(update)
	if err != nil || len(data) == 0 {
		return
	}
	if len(data) > maxCodeUpdateBytes {
		from.sendJSON(map[string]any{"type": "code.reject", "cid": cid, "message": "Слишком большое изменение (максимум 256 КБ за раз)"})
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.bytes+len(data) > maxCodeDocBytes {
		from.sendJSON(map[string]any{"type": "code.reject", "cid": cid, "message": "Документ кода слишком большой — удалите лишнее"})
		return
	}
	r.code.seq++
	seq := r.code.seq
	r.code.entries = append(r.code.entries, codeEntry{seq: seq, data: data})
	r.code.bytes += len(data)
	r.code.dirty = true

	b, _ := json.Marshal(map[string]any{"type": "code.update", "update": update, "seq": seq})
	for o := range r.clients {
		if o != from {
			o.enqueue(b)
		}
	}
	from.sendJSON(map[string]any{"type": "code.ack", "cid": cid, "seq": seq})

	pending := r.code.compactFrom != nil && time.Since(r.code.compactAt) < compactTimeout
	if !pending && (len(r.code.entries) > compactEntries || r.code.bytes > compactBytes) && len(r.code.entries) > 1 {
		r.code.compactFrom, r.code.compactUpTo, r.code.compactAt = from, seq, time.Now()
		from.sendJSON(map[string]any{"type": "code.compact.request", "upTo": seq})
	}
}

func (h *Hub) codeCompact(r *room, from *Client, upTo int64, state string) {
	data, err := base64.StdEncoding.DecodeString(state)
	if err != nil || len(data) == 0 || len(data) > maxCodeDocBytes {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.compactFrom != from || r.code.compactUpTo != upTo {
		return // не запрашивали у этого клиента — игнорируем
	}
	r.code.compactFrom = nil
	keep := []codeEntry{{seq: upTo, data: data}}
	total := len(data)
	for _, e := range r.code.entries {
		if e.seq > upTo {
			keep = append(keep, e)
			total += len(e.data)
		}
	}
	r.code.entries, r.code.bytes, r.code.dirty = keep, total, true
}
