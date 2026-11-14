package realtime

// Совместные документы Yjs урока: редактор кода (тексты «code» и «stdin», карта «meta»)
// и учебный текст мастерской (текст «text»). Это РАЗНЫЕ документы: свой журнал, своя таблица,
// свой префикс сообщений («code.*» и «workshop.*»). Механизм синхронизации общий.
//
// Сервер не разбирает Yjs — он ретранслятор с журналом. Каждое бинарное обновление Yjs получает
// порядковый номер seq, сохраняется и рассылается остальным участникам; отправитель получает
// подтверждение (<prefix>.ack). Обновления Yjs коммутативны и идемпотентны, поэтому порядок доставки
// и повторы не важны, а одновременные правки сливаются без потери текста. Клиент применяет чужие
// обновления с отдельным origin и не отправляет их обратно — бесконечного обмена нет.
//
// Новому клиенту (и после переподключения) отправляется весь журнал (<prefix>.snapshot); клиент в ответ
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

// docKind описывает вид совместного документа. table — имя таблицы из кода (не из ввода пользователя).
type docKind struct {
	prefix   string
	table    string
	tooBig   string
	lockable bool // правки отклоняются после закрытия урока (CloseLesson) или окончания окна редактирования клиента
}

var (
	codeKind     = &docKind{prefix: "code", table: "code_docs", tooBig: "Документ кода слишком большой — удалите лишнее"}
	workshopKind = &docKind{prefix: "workshop", table: "workshop_docs", tooBig: "Учебный текст слишком большой — удалите лишнее", lockable: true}
	docKinds     = []*docKind{codeKind, workshopKind}
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

func (h *Hub) loadDoc(ctx context.Context, kind *docKind, lessonID string) *codeState {
	st := &codeState{}
	var ups [][]byte
	if err := h.db.QueryRow(ctx, `SELECT updates, seq FROM `+kind.table+` WHERE lesson_id=$1`, lessonID).Scan(&ups, &st.seq); err != nil {
		return st
	}
	for _, u := range ups {
		st.entries = append(st.entries, codeEntry{seq: st.seq, data: u})
		st.bytes += len(u)
	}
	return st
}

func (r *room) doc(kind *docKind) *codeState {
	if kind == workshopKind {
		return r.workshop
	}
	return r.code
}

func (r *room) docsDirtyLocked() bool {
	return r.code.dirty || r.workshop.dirty
}

func (h *Hub) persistDocs(r *room) {
	for _, k := range docKinds {
		h.persistDoc(r, k)
	}
}

func (h *Hub) persistDoc(r *room, kind *docKind) {
	r.mu.Lock()
	d := r.doc(kind)
	if !d.dirty {
		r.mu.Unlock()
		return
	}
	ups := make([][]byte, len(d.entries))
	for i, e := range d.entries {
		ups[i] = e.data
	}
	seq := d.seq
	d.dirty = false
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.db.Exec(ctx, `INSERT INTO `+kind.table+`(lesson_id, updates, seq, updated_at) VALUES ($1,$2,$3, now())
		ON CONFLICT (lesson_id) DO UPDATE SET updates=$2, seq=$3, updated_at=now() WHERE `+kind.table+`.seq <= $3`, r.id, ups, seq)
	if err != nil {
		// Содержимое документа (код, учебный текст) в лог не пишем.
		slog.Error("persist doc", "kind", kind.prefix, "lesson", r.id, "err", err)
		r.mu.Lock()
		d.dirty = true
		r.mu.Unlock()
	}
}

// WorkshopDocUpdates возвращает журнал обновлений учебного текста (из памяти, если комната активна,
// иначе из БД) — для просмотра сохранённого текста на странице урока.
func (h *Hub) WorkshopDocUpdates(ctx context.Context, lessonID string) ([]string, int64) {
	h.mu.Lock()
	r := h.rooms[lessonID]
	h.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return encodeUpdates(r.workshop.entries), r.workshop.seq
	}
	st := h.loadDoc(ctx, workshopKind, lessonID)
	return encodeUpdates(st.entries), st.seq
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

func (h *Hub) docSync(r *room, kind *docKind, c *Client) {
	r.mu.Lock()
	d := r.doc(kind)
	msg := map[string]any{"type": kind.prefix + ".snapshot", "updates": encodeUpdates(d.entries), "seq": d.seq}
	if kind.lockable {
		msg["readOnly"] = r.lockedFor(c, time.Now())
	}
	r.mu.Unlock()
	c.sendJSON(msg)
}

// lockedFor — правки документа с блокировкой запрещены: урок закрыт или окно редактирования клиента истекло.
func (r *room) lockedFor(c *Client, now time.Time) bool {
	return r.closed || (!c.EditUntil.IsZero() && !now.Before(c.EditUntil))
}

func (h *Hub) docUpdate(r *room, kind *docKind, from *Client, update, cid string) {
	data, err := base64.StdEncoding.DecodeString(update)
	if err != nil || len(data) == 0 {
		return
	}
	if len(data) > maxCodeUpdateBytes {
		from.sendJSON(map[string]any{"type": kind.prefix + ".reject", "cid": cid, "message": "Слишком большое изменение (максимум 256 КБ за раз)"})
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind.lockable && r.lockedFor(from, time.Now()) {
		from.sendJSON(map[string]any{"type": kind.prefix + ".reject", "cid": cid, "locked": true, "message": "Урок завершён — совместное редактирование закрыто"})
		return
	}
	d := r.doc(kind)
	if d.bytes+len(data) > maxCodeDocBytes {
		from.sendJSON(map[string]any{"type": kind.prefix + ".reject", "cid": cid, "message": kind.tooBig})
		return
	}
	d.seq++
	seq := d.seq
	d.entries = append(d.entries, codeEntry{seq: seq, data: data})
	d.bytes += len(data)
	d.dirty = true

	b, _ := json.Marshal(map[string]any{"type": kind.prefix + ".update", "update": update, "seq": seq})
	for o := range r.clients {
		if o != from {
			o.enqueue(b)
		}
	}
	from.sendJSON(map[string]any{"type": kind.prefix + ".ack", "cid": cid, "seq": seq})

	pending := d.compactFrom != nil && time.Since(d.compactAt) < compactTimeout
	if !pending && (len(d.entries) > compactEntries || d.bytes > compactBytes) && len(d.entries) > 1 {
		d.compactFrom, d.compactUpTo, d.compactAt = from, seq, time.Now()
		from.sendJSON(map[string]any{"type": kind.prefix + ".compact.request", "upTo": seq})
	}
}

func (h *Hub) docCompact(r *room, kind *docKind, from *Client, upTo int64, state string) {
	data, err := base64.StdEncoding.DecodeString(state)
	if err != nil || len(data) == 0 || len(data) > maxCodeDocBytes {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.doc(kind)
	if d.compactFrom != from || d.compactUpTo != upTo {
		return // не запрашивали у этого клиента — игнорируем
	}
	d.compactFrom = nil
	keep := []codeEntry{{seq: upTo, data: data}}
	total := len(data)
	for _, e := range d.entries {
		if e.seq > upTo {
			keep = append(keep, e)
			total += len(e.data)
		}
	}
	d.entries, d.bytes, d.dirty = keep, total, true
}

// CloseLesson запрещает правки документов с блокировкой (учебный текст) в активной комнате урока.
// Вызывается при завершении и отмене урока; сохранённые документы не удаляются.
func (h *Hub) CloseLesson(lessonID string) {
	h.mu.Lock()
	r := h.rooms[lessonID]
	h.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}
