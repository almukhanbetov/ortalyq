package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const linkTTL = 10 * time.Minute

// Разрешённые типы файлов: расширение → (Content-Type, допустимые результаты сигнатурной проверки).
var allowedFiles = map[string]struct {
	ctype string
	sniff []string
}{
	".pdf":  {"application/pdf", []string{"application/pdf"}},
	".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", []string{"application/zip"}},
	".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []string{"application/zip"}},
	".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", []string{"application/zip"}},
	".doc":  {"application/msword", []string{"application/octet-stream"}},
	".xls":  {"application/vnd.ms-excel", []string{"application/octet-stream"}},
	".ppt":  {"application/vnd.ms-powerpoint", []string{"application/octet-stream"}},
	".txt":  {"text/plain; charset=utf-8", []string{"text/plain; charset=utf-8", "text/plain; charset=utf-16le", "text/plain; charset=utf-16be"}},
	".md":   {"text/markdown; charset=utf-8", []string{"text/plain; charset=utf-8"}},
	".png":  {"image/png", []string{"image/png"}},
	".jpg":  {"image/jpeg", []string{"image/jpeg"}},
	".jpeg": {"image/jpeg", []string{"image/jpeg"}},
	".webp": {"image/webp", []string{"image/webp"}},
	".mp3":  {"audio/mpeg", []string{"audio/mpeg", "application/octet-stream"}},
}

func allowedExtList() string {
	return "PDF, DOC(X), XLS(X), PPT(X), TXT, MD, PNG, JPG, WEBP, MP3"
}

type uploaded struct {
	Name, Key, Type string
	Size            int64
}

// receiveFile читает файл из multipart-формы, проверяет размер, расширение и сигнатуру и кладёт его в S3.
// Возвращает nil без ошибки, если поле отсутствует и optional=true.
func (s *Server) receiveFile(c *gin.Context, field, prefix string, optional bool) (*uploaded, bool) {
	fh, err := c.FormFile(field)
	if err != nil {
		if optional && err == http.ErrMissingFile {
			return nil, true
		}
		fail(c, 400, "Файл не получен")
		return nil, false
	}
	if s.store == nil {
		fail(c, 503, "Хранилище файлов не настроено (S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY)")
		return nil, false
	}
	max := int64(s.cfg.MaxUploadMB) << 20
	if fh.Size > max {
		fail(c, 413, fmt.Sprintf("Файл больше %d МБ", s.cfg.MaxUploadMB))
		return nil, false
	}
	name := filepath.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
	if name == "" || name == "." || utf8.RuneCountInString(name) > 150 {
		fail(c, 400, "Некорректное имя файла")
		return nil, false
	}
	ext := strings.ToLower(filepath.Ext(name))
	rule, ok := allowedFiles[ext]
	if !ok {
		fail(c, 415, "Недопустимый тип файла. Разрешены: "+allowedExtList())
		return nil, false
	}
	f, err := fh.Open()
	if err != nil {
		internal(c, err)
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		internal(c, err)
		return nil, false
	}
	if int64(len(data)) > max {
		fail(c, 413, fmt.Sprintf("Файл больше %d МБ", s.cfg.MaxUploadMB))
		return nil, false
	}
	if len(data) == 0 {
		fail(c, 400, "Файл пустой")
		return nil, false
	}
	sniffed := http.DetectContentType(data)
	match := false
	for _, v := range rule.sniff {
		if v == sniffed {
			match = true
		}
	}
	if !match {
		fail(c, 415, "Содержимое файла не соответствует расширению "+ext)
		return nil, false
	}
	key := fmt.Sprintf("%s/%d%s", prefix, time.Now().UnixNano(), ext)
	if err := s.store.Put(c, key, bytes.NewReader(data), int64(len(data)), rule.ctype); err != nil {
		internal(c, err)
		return nil, false
	}
	return &uploaded{Name: name, Key: key, Type: rule.ctype, Size: int64(len(data))}, true
}

type linkPayload struct {
	Key    string `json:"k"`
	Name   string `json:"n"`
	Type   string `json:"t"`
	Inline bool   `json:"i"`
}

// signedLink — временная ссылка на объект. Выдаётся только после проверки прав вызывающим обработчиком.
func (s *Server) signedLink(key, name, ctype string) string {
	inline := strings.HasPrefix(ctype, "image/") || strings.HasPrefix(ctype, "video/") || strings.HasPrefix(ctype, "audio/") || ctype == "application/pdf"
	b, _ := json.Marshal(linkPayload{Key: key, Name: name, Type: ctype, Inline: inline})
	return "/api/files/" + s.signer.Sign("file", string(b), linkTTL)
}

func (s *Server) serveSignedFile(c *gin.Context) {
	payload, err := s.signer.Verify("file", c.Param("token"))
	if err != nil {
		fail(c, 403, "Ссылка недействительна или устарела. Откройте файл заново со страницы занятия.")
		return
	}
	var p linkPayload
	if json.Unmarshal([]byte(payload), &p) != nil || s.store == nil {
		fail(c, 403, "Ссылка недействительна")
		return
	}
	obj, info, err := s.store.Open(c, p.Key)
	if err != nil {
		fail(c, 404, "Файл не найден")
		return
	}
	defer obj.Close()
	disp := "attachment"
	if p.Inline && c.Query("download") == "" {
		disp = "inline"
	}
	c.Header("Content-Type", p.Type)
	c.Header("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": p.Name})+"; filename*=UTF-8''"+url.PathEscape(p.Name))
	c.Header("Cache-Control", "private, max-age=600")
	http.ServeContent(c.Writer, c.Request, "", info.LastModified, obj)
}

// ---- Материалы занятия ----

func (s *Server) lessonMaterials(c *gin.Context, lessonID string) ([]gin.H, error) {
	rows, err := s.db.Query(c, `SELECT m.id, m.filename, m.content_type, m.size_bytes, m.created_at, u.name
		FROM materials m JOIN users u ON u.id = m.uploader_id WHERE m.lesson_id=$1 ORDER BY m.created_at`, lessonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name, ct, by string
		var size int64
		var at time.Time
		if err := rows.Scan(&id, &name, &ct, &size, &at, &by); err != nil {
			return nil, err
		}
		out = append(out, gin.H{"id": id, "filename": name, "contentType": ct, "size": size, "createdAt": at, "uploadedBy": by})
	}
	return out, rows.Err()
}

func (s *Server) uploadMaterial(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Материалы добавляет преподаватель")
		return
	}
	up, ok := s.receiveFile(c, "file", "lessons/"+l.ID+"/materials", false)
	if !ok {
		return
	}
	var id string
	if err := s.db.QueryRow(c, `INSERT INTO materials(lesson_id, uploader_id, filename, object_key, content_type, size_bytes) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		l.ID, currentUser(c).ID, up.Name, up.Key, up.Type, up.Size).Scan(&id); err != nil {
		internal(c, err)
		return
	}
	s.hub.Broadcast(l.ID, map[string]any{"type": "materials.changed"})
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) deleteMaterial(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Недостаточно прав")
		return
	}
	var key string
	err := s.db.QueryRow(c, `DELETE FROM materials WHERE id=$1 AND lesson_id=$2 RETURNING object_key`, c.Param("mid"), l.ID).Scan(&key)
	if err != nil {
		fail(c, 404, "Файл не найден")
		return
	}
	if s.store != nil {
		s.store.Delete(c, key)
	}
	s.hub.Broadcast(l.ID, map[string]any{"type": "materials.changed"})
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) materialLink(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var name, key, ct string
	if !isUUID(c.Param("mid")) || s.db.QueryRow(c, `SELECT filename, object_key, content_type FROM materials WHERE id=$1 AND lesson_id=$2`, c.Param("mid"), l.ID).Scan(&name, &key, &ct) != nil {
		fail(c, 404, "Файл не найден")
		return
	}
	c.JSON(200, gin.H{"url": s.signedLink(key, name, ct), "expiresIn": int(linkTTL.Seconds())})
}

// ---- Домашние задания ----

func (s *Server) lessonHomework(c *gin.Context, lessonID string) ([]gin.H, error) {
	rows, err := s.db.Query(c, `SELECT h.id, h.title, h.description, h.due_at, h.file_name, h.file_size, h.created_at,
		sb.id, sb.body, sb.file_name, sb.file_size, sb.status, sb.teacher_comment, sb.grade, sb.submitted_at, sb.reviewed_at
		FROM homeworks h LEFT JOIN homework_submissions sb ON sb.homework_id = h.id
		WHERE h.lesson_id=$1 ORDER BY h.created_at`, lessonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, title, desc string
		var due *time.Time
		var fname *string
		var fsize *int64
		var created time.Time
		var sid, sbody, sfname, sstatus, scomment *string
		var sfsize *int64
		var sgrade *int
		var ssub, srev *time.Time
		if err := rows.Scan(&id, &title, &desc, &due, &fname, &fsize, &created, &sid, &sbody, &sfname, &sfsize, &sstatus, &scomment, &sgrade, &ssub, &srev); err != nil {
			return nil, err
		}
		hw := gin.H{"id": id, "title": title, "description": desc, "dueAt": due, "fileName": fname, "fileSize": fsize, "createdAt": created, "submission": nil}
		if sid != nil {
			hw["submission"] = gin.H{"id": *sid, "body": *sbody, "fileName": sfname, "fileSize": sfsize, "status": *sstatus,
				"teacherComment": *scomment, "grade": sgrade, "submittedAt": ssub, "reviewedAt": srev}
		}
		out = append(out, hw)
	}
	return out, rows.Err()
}

func (s *Server) myHomework(c *gin.Context) {
	u := currentUser(c)
	col := "l.student_id"
	if u.Role == "teacher" {
		col = "l.teacher_id"
	} else if u.Role != "student" {
		c.JSON(200, gin.H{"homework": []any{}})
		return
	}
	rows, err := s.db.Query(c, `SELECT h.id, h.title, h.due_at, h.created_at, l.id, l.subject, l.starts_at, t.name, st.name, sb.status
		FROM homeworks h JOIN lessons l ON l.id = h.lesson_id JOIN users t ON t.id=l.teacher_id JOIN users st ON st.id=l.student_id
		LEFT JOIN homework_submissions sb ON sb.homework_id = h.id
		WHERE `+col+`=$1 ORDER BY (sb.status IS NULL) DESC, h.created_at DESC LIMIT 100`, u.ID)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, title, lid, subject, tname, sname string
		var due *time.Time
		var created, lstart time.Time
		var status *string
		if err := rows.Scan(&id, &title, &due, &created, &lid, &subject, &lstart, &tname, &sname, &status); err != nil {
			internal(c, err)
			return
		}
		st := "assigned"
		if status != nil {
			st = *status
		}
		out = append(out, gin.H{"id": id, "title": title, "dueAt": due, "createdAt": created, "lessonId": lid, "subject": subject,
			"lessonStartsAt": lstart, "teacherName": tname, "studentName": sname, "status": st})
	}
	c.JSON(200, gin.H{"homework": out})
}

func (s *Server) createHomework(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Задание выдаёт преподаватель")
		return
	}
	if l.Status == "cancelled" {
		fail(c, 409, "Занятие отменено")
		return
	}
	title := strings.TrimSpace(c.PostForm("title"))
	desc := strings.TrimSpace(c.PostForm("description"))
	if n := utf8.RuneCountInString(title); n < 3 || n > 150 {
		fail(c, 400, "Название задания: от 3 до 150 символов")
		return
	}
	if utf8.RuneCountInString(desc) > 5000 {
		fail(c, 400, "Описание слишком длинное")
		return
	}
	var due *time.Time
	if v := c.PostForm("dueAt"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fail(c, 400, "Некорректный срок сдачи")
			return
		}
		t = t.UTC()
		due = &t
	}
	up, ok := s.receiveFile(c, "file", "lessons/"+l.ID+"/homework", true)
	if !ok {
		return
	}
	var fname, fkey, ftype *string
	var fsize *int64
	if up != nil {
		fname, fkey, ftype, fsize = &up.Name, &up.Key, &up.Type, &up.Size
	}
	var id string
	if err := s.db.QueryRow(c, `INSERT INTO homeworks(lesson_id, title, description, due_at, file_name, file_key, file_type, file_size)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, l.ID, title, desc, due, fname, fkey, ftype, fsize).Scan(&id); err != nil {
		internal(c, err)
		return
	}
	s.hub.Broadcast(l.ID, map[string]any{"type": "homework.changed"})
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) deleteHomework(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Недостаточно прав")
		return
	}
	if !isUUID(c.Param("hid")) {
		fail(c, 404, "Задание не найдено")
		return
	}
	var fkey, skey *string
	err := s.db.QueryRow(c, `SELECT h.file_key, sb.file_key FROM homeworks h LEFT JOIN homework_submissions sb ON sb.homework_id=h.id WHERE h.id=$1 AND h.lesson_id=$2`,
		c.Param("hid"), l.ID).Scan(&fkey, &skey)
	if err != nil {
		fail(c, 404, "Задание не найдено")
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM homeworks WHERE id=$1`, c.Param("hid")); err != nil {
		internal(c, err)
		return
	}
	for _, k := range []*string{fkey, skey} {
		if k != nil && s.store != nil {
			s.store.Delete(c, *k)
		}
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) homeworkFileLink(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var name, key, ct *string
	if !isUUID(c.Param("hid")) || s.db.QueryRow(c, `SELECT file_name, file_key, file_type FROM homeworks WHERE id=$1 AND lesson_id=$2`, c.Param("hid"), l.ID).Scan(&name, &key, &ct) != nil || key == nil {
		fail(c, 404, "Файл не найден")
		return
	}
	c.JSON(200, gin.H{"url": s.signedLink(*key, *name, *ct), "expiresIn": int(linkTTL.Seconds())})
}

func (s *Server) submitHomework(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "student" {
		fail(c, 403, "Ответ отправляет ученик")
		return
	}
	hid := c.Param("hid")
	var exists bool
	if !isUUID(hid) || s.db.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM homeworks WHERE id=$1 AND lesson_id=$2)`, hid, l.ID).Scan(&exists) != nil || !exists {
		fail(c, 404, "Задание не найдено")
		return
	}
	var status string
	err := s.db.QueryRow(c, `SELECT status FROM homework_submissions WHERE homework_id=$1`, hid).Scan(&status)
	if err == nil && status == "reviewed" {
		fail(c, 409, "Работа уже проверена — изменить ответ нельзя")
		return
	}
	body := strings.TrimSpace(c.PostForm("body"))
	if utf8.RuneCountInString(body) > 10000 {
		fail(c, 400, "Ответ слишком длинный")
		return
	}
	up, ok := s.receiveFile(c, "file", "lessons/"+l.ID+"/submissions", true)
	if !ok {
		return
	}
	if body == "" && up == nil {
		fail(c, 400, "Напишите ответ или прикрепите файл")
		return
	}
	var oldKey *string
	s.db.QueryRow(c, `SELECT file_key FROM homework_submissions WHERE homework_id=$1`, hid).Scan(&oldKey)
	if up != nil {
		_, err = s.db.Exec(c, `INSERT INTO homework_submissions(homework_id, student_id, body, file_name, file_key, file_type, file_size)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (homework_id) DO UPDATE SET body=$3, file_name=$4, file_key=$5, file_type=$6, file_size=$7, submitted_at=now()`,
			hid, l.StudentID, body, up.Name, up.Key, up.Type, up.Size)
	} else {
		_, err = s.db.Exec(c, `INSERT INTO homework_submissions(homework_id, student_id, body) VALUES ($1,$2,$3)
			ON CONFLICT (homework_id) DO UPDATE SET body=$3, submitted_at=now()`, hid, l.StudentID, body)
	}
	if err != nil {
		internal(c, err)
		return
	}
	if up != nil && oldKey != nil && s.store != nil {
		s.store.Delete(c, *oldKey)
	}
	s.hub.Broadcast(l.ID, map[string]any{"type": "homework.changed"})
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) submissionFileLink(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var name, key, ct *string
	err := s.db.QueryRow(c, `SELECT sb.file_name, sb.file_key, sb.file_type FROM homework_submissions sb JOIN homeworks h ON h.id = sb.homework_id
		WHERE h.id=$1 AND h.lesson_id=$2`, c.Param("hid"), l.ID).Scan(&name, &key, &ct)
	if !isUUID(c.Param("hid")) || err != nil || key == nil {
		fail(c, 404, "Файл не найден")
		return
	}
	c.JSON(200, gin.H{"url": s.signedLink(*key, *name, *ct), "expiresIn": int(linkTTL.Seconds())})
}

func (s *Server) reviewHomework(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Проверяет преподаватель")
		return
	}
	var req struct {
		Comment string `json:"comment"`
		Grade   *int   `json:"grade"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if req.Grade != nil && (*req.Grade < 1 || *req.Grade > 5) {
		fail(c, 400, "Оценка от 1 до 5")
		return
	}
	if utf8.RuneCountInString(req.Comment) > 5000 {
		fail(c, 400, "Комментарий слишком длинный")
		return
	}
	if !isUUID(c.Param("hid")) {
		fail(c, 404, "Задание не найдено")
		return
	}
	tag, err := s.db.Exec(c, `UPDATE homework_submissions sb SET status='reviewed', teacher_comment=$1, grade=$2, reviewed_at=now()
		FROM homeworks h WHERE h.id = sb.homework_id AND h.id=$3 AND h.lesson_id=$4`, strings.TrimSpace(req.Comment), req.Grade, c.Param("hid"), l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "Ученик ещё не отправил ответ")
		return
	}
	s.hub.Broadcast(l.ID, map[string]any{"type": "homework.changed"})
	c.JSON(200, gin.H{"ok": true})
}
