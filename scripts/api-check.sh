#!/usr/bin/env bash
# Автоматическая проверка ключевых сценариев API на демо-данных.
# Требует: запущенный бэкенд, выполненный seed, curl, jq.
#   API=http://localhost:18080 DEMO_PASSWORD=... ./scripts/api-check.sh
set -uo pipefail
API=${API:-http://localhost:18080}
PW=${DEMO_PASSWORD:?задайте DEMO_PASSWORD}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
pass=0; failn=0
ok()   { echo "  ✔ $1"; pass=$((pass+1)); }
bad()  { echo "  ✘ $1"; failn=$((failn+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (ожидалось $3, получено $2)"; fi; }

login() { # $1 jar, $2 email
  curl -s -c "$TMP/$1" -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "{\"email\":\"$2\",\"password\":\"$PW\"}" "$API/api/auth/login"
}
req() { # $1 jar, $2 method, $3 path, [$4 json]
  if [ -n "${4:-}" ]; then
    curl -s -b "$TMP/$1" -X "$2" -H 'Content-Type: application/json' -d "$4" -w '\n%{http_code}' "$API$3"
  else
    curl -s -b "$TMP/$1" -X "$2" -w '\n%{http_code}' "$API$3"
  fi
}
code() { tail -n1; }
body() { sed '$d'; }

echo "1. Авторизация"
check "вход преподавателя" "$(login t teacher@demo.ortalyq.local)" 200
check "вход ученика" "$(login s student@demo.ortalyq.local)" 200
check "вход второго ученика" "$(login o student2@demo.ortalyq.local)" 200
check "неверный пароль отклонён" "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{"email":"student@demo.ortalyq.local","password":"wrong-password"}' $API/api/auth/login)" 401
check "без входа — 401" "$(curl -s -o /dev/null -w '%{http_code}' $API/api/lessons)" 401
check "чужой Origin — 403 (CSRF)" "$(curl -s -b $TMP/s -o /dev/null -w '%{http_code}' -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -X POST -d '{}' $API/api/listings)" 403

echo "2. Каталог и объявления"
TEACHER_ID=$(req s GET /api/teachers?subject=%D0%9C%D0%B0%D1%82%D0%B5%D0%BC%D0%B0%D1%82%D0%B8%D0%BA%D0%B0 | body | jq -r '.teachers[] | select(.name|startswith("Айгерим Нурланова")) | .id')
[ -n "$TEACHER_ID" ] && ok "фильтр по предмету находит преподавателя" || bad "фильтр по предмету"
N=$(req s GET "/api/teachers?maxPrice=6000" | body | jq '[.teachers[] | select(.price > 6000)] | length')
check "фильтр по цене (до 6 000 ₸)" "$N" 0
CITY=$(req s GET "/api/teachers?city=%D0%90%D1%81%D1%82%D0%B0%D0%BD%D0%B0" | body | jq -r '[.teachers[].city] | unique | join(",")')
check "фильтр по городу (Астана)" "$CITY" "Астана"
check "телефон не в формате +7 отклонён" "$(req s PATCH /api/me '{"phone":"12345"}' | code)" 400
LID=$(req s POST /api/listings '{"subject":"Физика","title":"Тестовая заявка","body":"Проверочное объявление для теста"}' | body | jq -r .id)
check "ученик создаёт заявку" "$(req s GET /api/my/listings | body | jq -r --arg id "$LID" '.listings[]|select(.id==$id)|.kind')" request
check "чужое объявление нельзя изменить" "$(req o PUT /api/listings/$LID '{"subject":"Физика","title":"Взлом","body":"Попытка изменить чужое"}' | code)" 403
check "ученик не может скрыть объявление" "$(req s POST /api/admin/listings/$LID/hide '{}' | code)" 403
check "удаление своего объявления" "$(req s DELETE /api/listings/$LID | code)" 200

echo "3. Бронирование и защита от двойной записи"
# Начало с нестандартными минутами: +7 мин от начала первого свободного интервала, где это помещается
SLOT=$(req s GET "/api/teachers/$TEACHER_ID/slots?duration=60" | body | jq -r '[.intervals[] | select((.lastStartAt|fromdate) - (.startsAt|fromdate) >= 420)][0].startsAt | fromdate + 420 | todate')
[ -n "$SLOT" ] && [ "$SLOT" != "null" ] && ok "есть свободное время (начало $SLOT)" || bad "нет свободных интервалов"
BODY="{\"teacherId\":\"$TEACHER_ID\",\"startsAt\":\"$SLOT\",\"duration\":60,\"subject\":\"Математика\"}"
# Одновременно отправляем 8 запросов от двух учеников на один и тот же слот
for i in 1 2 3 4 5 6 7 8; do j=s; [ $((i % 2)) = 0 ] && j=o
  (printf '%s\n' "$(req $j POST /api/lessons "$BODY" | code)" > "$TMP/book_$i") & done; wait
CREATED=$(cat "$TMP"/book_* | grep -c '^201$'); CONFLICT=$(cat "$TMP"/book_* | grep -c '^409$')
check "8 одновременных запросов: создано ровно одно занятие" "$CREATED/$CONFLICT" "1/7"
check "повторное бронирование занятого слота" "$(req s POST /api/lessons "$BODY" | code)" 409

echo "4. Права доступа к занятию"
DEMO_LESSON=$(req s GET "/api/lessons?scope=upcoming" | body | jq -r '[.lessons[] | select(.note|startswith("Демо-класс"))][0].id')
[ -n "$DEMO_LESSON" ] && [ "$DEMO_LESSON" != "null" ] && ok "демо-занятие найдено" || bad "демо-занятие не найдено"
check "участник видит занятие" "$(req s GET /api/lessons/$DEMO_LESSON | code)" 200
check "посторонний не видит занятие" "$(req o GET /api/lessons/$DEMO_LESSON | code)" 404
check "посторонний не получает токен комнаты" "$(req o POST /api/lessons/$DEMO_LESSON/join | code)" 404
check "посторонний не читает доску" "$(req o GET /api/lessons/$DEMO_LESSON/board | code)" 404
check "посторонний не читает чат" "$(req o GET /api/lessons/$DEMO_LESSON/chat | code)" 404
check "ученик не может начать запись" "$(req s POST /api/lessons/$DEMO_LESSON/recordings | code)" 403
check "ученик не может завершить занятие" "$(req s POST /api/lessons/$DEMO_LESSON/end | code)" 403

echo "5. Домашнее задание: назначение → ответ → проверка"
printf 'Задача 1: решите уравнение x^2 = 4\n' > "$TMP/task.txt"
HW=$(curl -s -b $TMP/t -F title="Тестовое задание" -F description="Решить" -F file=@"$TMP/task.txt" $API/api/lessons/$DEMO_LESSON/homework | jq -r .id)
[ "$HW" != "null" ] && ok "преподаватель выдал задание с файлом" || bad "задание не создано"
printf '<html>' > "$TMP/evil.exe"
check "недопустимый тип файла отклонён" "$(curl -s -b $TMP/t -o /dev/null -w '%{http_code}' -F file=@$TMP/evil.exe $API/api/lessons/$DEMO_LESSON/materials)" 415
printf '%%PDF-fake\n' > "$TMP/fake.docx"
check "подмена расширения отклонена" "$(curl -s -b $TMP/t -o /dev/null -w '%{http_code}' -F file=@$TMP/fake.docx $API/api/lessons/$DEMO_LESSON/materials)" 415
LINK=$(req s GET /api/lessons/$DEMO_LESSON/homework/$HW/link | body | jq -r .url)
check "ученик скачивает файл задания по временной ссылке" "$(curl -s -o /dev/null -w '%{http_code}' "$API$LINK")" 200
check "посторонний не получает ссылку" "$(req o GET /api/lessons/$DEMO_LESSON/homework/$HW/link | code)" 404
check "подделанная ссылка отклонена" "$(curl -s -o /dev/null -w '%{http_code}' "$API${LINK%?}X")" 403
check "ответ ученика" "$(curl -s -b $TMP/s -o /dev/null -w '%{http_code}' -F body="x = ±2" $API/api/lessons/$DEMO_LESSON/homework/$HW/submission)" 200
check "посторонний не может ответить" "$(curl -s -b $TMP/o -o /dev/null -w '%{http_code}' -F body="x" $API/api/lessons/$DEMO_LESSON/homework/$HW/submission)" 404
check "проверка преподавателем" "$(req t POST /api/lessons/$DEMO_LESSON/homework/$HW/review '{"comment":"Верно","grade":5}' | code)" 200
check "статус «проверено» виден ученику" "$(req s GET /api/lessons/$DEMO_LESSON | body | jq -r --arg h "$HW" '.homework[]|select(.id==$h)|.submission.status')" reviewed
check "после проверки ответ не изменить" "$(curl -s -b $TMP/s -o /dev/null -w '%{http_code}' -F body="другое" $API/api/lessons/$DEMO_LESSON/homework/$HW/submission)" 409
req t DELETE /api/lessons/$DEMO_LESSON/homework/$HW >/dev/null

echo "6. Отзыв"
DONE=$(req s GET "/api/lessons?scope=history" | body | jq -r '[.lessons[]|select(.status=="completed" and .subject=="Физика")][0].id')
check "ученик оставляет отзыв о завершённом занятии" "$(req s POST /api/lessons/$DONE/review '{"rating":5,"body":"Тест"}' | code)" 201
check "повторный отзыв запрещён" "$(req s POST /api/lessons/$DONE/review '{"rating":4}' | code)" 409
check "отзыв о незавершённом занятии запрещён" "$(req s POST /api/lessons/$DEMO_LESSON/review '{"rating":5}' | code)" 409

echo
echo "Итого: успешно $pass, ошибок $failn"
[ "$failn" = 0 ]
