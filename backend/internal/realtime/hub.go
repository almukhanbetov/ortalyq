// Package realtime — комнаты занятий: синхронизация доски, чат и присутствие по WebSocket.
//
// Доска хранится поэлементно (элементы Excalidraw). Каждый элемент имеет version и versionNonce;
// сервер принимает изменение, только если оно новее (как в Excalidraw: бо́льшая версия побеждает,
// при равных версиях — меньший versionNonce). Поэтому порядок доставки не важен, а после
// переподключения клиент получает полное актуальное состояние.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxElements      = 8000
	maxElementBytes  = 512 << 10
	sendBuffer       = 512
	persistInterval  = 1500 * time.Millisecond
	chatHistoryLimit = 300
)

type elemMeta struct {
	ID           string `json:"id"`
	Version      int64  `json:"version"`
	VersionNonce int64  `json:"versionNonce"`
	IsDeleted    bool   `json:"isDeleted"`
}

type element struct {
	meta elemMeta
	raw  json.RawMessage
}

type Client struct {
	UserID string
	Name   string
	Role   string
	send   chan []byte
	conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

type room struct {
	id       string
	mu       sync.Mutex
	clients  map[*Client]bool
	elements map[string]*element
	order    []string
	revision int64
	dirty    bool
	code     codeState
}

type Hub struct {
	db    *pgxpool.Pool
	mu    sync.Mutex
	rooms map[string]*room
}

func NewHub(db *pgxpool.Pool) *Hub {
	return &Hub{db: db, rooms: map[string]*room{}}
}

// Run периодически сохраняет изменённые доски.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(persistInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.flushAll()
			return
		case <-t.C:
			h.flushAll()
		}
	}
}

func (h *Hub) flushAll() {
	h.mu.Lock()
	list := make([]*room, 0, len(h.rooms))
	for _, r := range h.rooms {
		list = append(list, r)
	}
	h.mu.Unlock()
	for _, r := range list {
		h.persist(r)
		h.persistCode(r)
	}
}

func (h *Hub) loadBoard(ctx context.Context, lessonID string) (map[string]*element, []string, int64) {
	elements := map[string]*element{}
	var order []string
	var raw []byte
	var rev int64
	if err := h.db.QueryRow(ctx, `SELECT elements, revision FROM boards WHERE lesson_id=$1`, lessonID).Scan(&raw, &rev); err != nil {
		return elements, order, 0
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return elements, order, rev
	}
	for _, e := range arr {
		var m elemMeta
		if json.Unmarshal(e, &m) == nil && m.ID != "" {
			if _, ok := elements[m.ID]; !ok {
				order = append(order, m.ID)
			}
			elements[m.ID] = &element{meta: m, raw: e}
		}
	}
	return elements, order, rev
}

func (h *Hub) getRoom(ctx context.Context, lessonID string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[lessonID]; ok {
		return r
	}
	els, order, rev := h.loadBoard(ctx, lessonID)
	r := &room{id: lessonID, clients: map[*Client]bool{}, elements: els, order: order, revision: rev, code: h.loadCode(ctx, lessonID)}
	h.rooms[lessonID] = r
	return r
}

func (r *room) snapshotLocked() []byte {
	arr := make([]json.RawMessage, 0, len(r.order))
	for _, id := range r.order {
		arr = append(arr, r.elements[id].raw)
	}
	b, _ := json.Marshal(arr)
	return b
}

func (h *Hub) persist(r *room) {
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return
	}
	data := r.snapshotLocked()
	rev := r.revision
	r.dirty = false
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.db.Exec(ctx, `INSERT INTO boards(lesson_id, elements, revision, updated_at) VALUES ($1,$2,$3, now())
		ON CONFLICT (lesson_id) DO UPDATE SET elements=$2, revision=$3, updated_at=now() WHERE boards.revision <= $3`, r.id, data, rev)
	if err != nil {
		slog.Error("persist board", "lesson", r.id, "err", err)
		r.mu.Lock()
		r.dirty = true
		r.mu.Unlock()
	}
}

// Flush немедленно сохраняет доску занятия (например, при завершении урока).
func (h *Hub) Flush(lessonID string) {
	h.mu.Lock()
	r := h.rooms[lessonID]
	h.mu.Unlock()
	if r != nil {
		h.persist(r)
		h.persistCode(r)
	}
}

// BoardSnapshot возвращает актуальные элементы доски (из памяти, если комната активна).
func (h *Hub) BoardSnapshot(ctx context.Context, lessonID string) ([]byte, int64) {
	h.mu.Lock()
	r := h.rooms[lessonID]
	h.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.snapshotLocked(), r.revision
	}
	var raw []byte
	var rev int64
	if err := h.db.QueryRow(ctx, `SELECT elements, revision FROM boards WHERE lesson_id=$1`, lessonID).Scan(&raw, &rev); err != nil {
		return []byte("[]"), 0
	}
	return raw, rev
}

func (h *Hub) Broadcast(lessonID string, msg any) {
	h.mu.Lock()
	r := h.rooms[lessonID]
	h.mu.Unlock()
	if r == nil {
		return
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		c.enqueue(b)
	}
}

func (c *Client) enqueue(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- b:
	default:
		// Клиент не успевает читать — закрываем соединение, он переподключится и получит снимок.
		c.closed = true
		close(c.send)
	}
}

func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

func (r *room) presenceLocked() []byte {
	seen := map[string]bool{}
	users := []map[string]string{}
	for c := range r.clients {
		if seen[c.UserID] {
			continue
		}
		seen[c.UserID] = true
		users = append(users, map[string]string{"id": c.UserID, "name": c.Name, "role": c.Role})
	}
	b, _ := json.Marshal(map[string]any{"type": "presence", "users": users})
	return b
}

type ChatStore func(ctx context.Context, lessonID, userID, body string) (map[string]any, error)

// Serve обслуживает одно WebSocket-соединение участника. hello — начальное состояние,
// которое дополняется снимком доски.
func (h *Hub) Serve(ctx context.Context, conn *websocket.Conn, lessonID string, c *Client, hello map[string]any, chat ChatStore) {
	conn.SetReadLimit(4 << 20)
	c.conn = conn
	c.send = make(chan []byte, sendBuffer)
	r := h.getRoom(ctx, lessonID)

	r.mu.Lock()
	r.clients[c] = true
	hello["type"] = "hello"
	hello["board"] = map[string]any{"elements": json.RawMessage(r.snapshotLocked()), "revision": r.revision}
	hb, _ := json.Marshal(hello)
	c.enqueue(hb)
	pres := r.presenceLocked()
	for o := range r.clients {
		o.enqueue(pres)
	}
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.writer(ctx, c)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		h.handle(ctx, r, c, data, chat)
	}

	r.mu.Lock()
	delete(r.clients, c)
	c.close()
	empty := len(r.clients) == 0
	pres = r.presenceLocked()
	for o := range r.clients {
		o.enqueue(pres)
	}
	r.mu.Unlock()
	if empty {
		h.persist(r)
		h.persistCode(r)
		h.mu.Lock()
		r.mu.Lock()
		if len(r.clients) == 0 && !r.dirty && !r.code.dirty {
			delete(h.rooms, lessonID)
		}
		r.mu.Unlock()
		h.mu.Unlock()
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func (h *Hub) writer(ctx context.Context, c *Client) {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-c.send:
			if !ok {
				c.conn.Close(websocket.StatusPolicyViolation, "slow consumer")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.conn.CloseNow()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.conn.CloseNow()
				return
			}
		}
	}
}

type inbound struct {
	Type     string            `json:"type"`
	Elements []json.RawMessage `json:"elements"`
	Body     string            `json:"body"`
	ClientID string            `json:"clientId"`
	Update   string            `json:"update"`
	Cid      string            `json:"cid"`
	UpTo     int64             `json:"upTo"`
	State    string            `json:"state"`
}

func (h *Hub) handle(ctx context.Context, r *room, c *Client, data []byte, chat ChatStore) {
	var msg inbound
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "ping":
		c.enqueue([]byte(`{"type":"pong"}`))
	case "board.update":
		h.applyBoard(r, c, msg.Elements)
	case "board.clear":
		h.clearBoard(r)
	case "board.sync":
		// Клиент (например, доска, загруженная позже сокета) запрашивает полный снимок.
		r.mu.Lock()
		b, _ := json.Marshal(map[string]any{"type": "board.snapshot", "board": map[string]any{"elements": json.RawMessage(r.snapshotLocked()), "revision": r.revision}})
		r.mu.Unlock()
		c.enqueue(b)
	case "code.sync":
		h.codeSync(r, c)
	case "code.update":
		h.codeUpdate(r, c, msg.Update, truncID(msg.Cid))
	case "code.compact":
		h.codeCompact(r, c, msg.UpTo, msg.State)
	case "chat.send":
		if chat == nil {
			return
		}
		m, err := chat(ctx, r.id, c.UserID, msg.Body)
		if err != nil {
			b, _ := json.Marshal(map[string]any{"type": "error", "message": err.Error(), "clientId": msg.ClientID})
			c.enqueue(b)
			return
		}
		m["clientId"] = msg.ClientID
		b, _ := json.Marshal(map[string]any{"type": "chat.message", "message": m})
		r.mu.Lock()
		for o := range r.clients {
			o.enqueue(b)
		}
		r.mu.Unlock()
	}
}

func (h *Hub) applyBoard(r *room, from *Client, incoming []json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accepted := make([]json.RawMessage, 0, len(incoming))
	for _, raw := range incoming {
		if len(raw) > maxElementBytes {
			continue
		}
		var m elemMeta
		if json.Unmarshal(raw, &m) != nil || m.ID == "" || len(m.ID) > 64 {
			continue
		}
		cur, exists := r.elements[m.ID]
		if exists {
			if m.Version < cur.meta.Version || (m.Version == cur.meta.Version && m.VersionNonce >= cur.meta.VersionNonce) {
				continue
			}
		} else {
			if len(r.order) >= maxElements {
				continue
			}
			r.order = append(r.order, m.ID)
		}
		cp := make(json.RawMessage, len(raw))
		copy(cp, raw)
		r.elements[m.ID] = &element{meta: m, raw: cp}
		accepted = append(accepted, cp)
	}
	if len(accepted) == 0 {
		return
	}
	r.revision++
	r.dirty = true
	b, _ := json.Marshal(map[string]any{"type": "board.update", "elements": accepted, "revision": r.revision})
	for o := range r.clients {
		if o != from {
			o.enqueue(b)
		}
	}
}

// clearBoard помечает все элементы удалёнными с увеличением версии — так очистка
// корректно сливается с параллельными изменениями.
func (h *Hub) clearBoard(r *room) {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := []json.RawMessage{}
	for _, id := range r.order {
		e := r.elements[id]
		if e.meta.IsDeleted {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(e.raw, &obj) != nil {
			continue
		}
		e.meta.Version++
		e.meta.VersionNonce = time.Now().UnixNano() % 2147483647
		e.meta.IsDeleted = true
		obj["version"] = e.meta.Version
		obj["versionNonce"] = e.meta.VersionNonce
		obj["isDeleted"] = true
		obj["updated"] = time.Now().UnixMilli()
		nb, _ := json.Marshal(obj)
		e.raw = nb
		changed = append(changed, nb)
	}
	r.revision++
	r.dirty = true
	b, _ := json.Marshal(map[string]any{"type": "board.update", "elements": changed, "revision": r.revision, "cleared": true})
	for o := range r.clients {
		o.enqueue(b)
	}
}

// ChatLimit — количество последних сообщений в начальном состоянии.
func ChatLimit() int { return chatHistoryLimit }

func truncID(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}
