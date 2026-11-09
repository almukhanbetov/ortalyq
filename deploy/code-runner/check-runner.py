#!/usr/bin/env python3
"""Приёмочная проверка реального исполнителя Judge0 для ORTALYQ (только стандартная библиотека Python).

Запускать с сервера, которому разрешён доступ (ALLOWED_IP — сервер приложения):
    python3 check-runner.py https://runner.example.org [python_language_id]
Ключ берётся из переменной CODE_RUNNER_TOKEN или запрашивается скрыто.

Проверяет: версию и настройки (сеть запрещена), Python 3, stdout, stdin, синтаксическую ошибку,
бесконечный цикл, лимит вывода, лимит памяти, лимит процессов, отсутствие сети, отсутствие секретов
в окружении и недоступность файлов исполнителя. Код возврата 0 — все проверки пройдены.
"""
import base64
import getpass
import json
import os
import sys
import time
import urllib.error
import urllib.request

LIMITS = dict(cpu_time_limit=2, cpu_extra_time=0.5, wall_time_limit=5, memory_limit=131072, stack_limit=64000,
              max_processes_and_or_threads=16, max_file_size=64, enable_network=False,
              enable_per_process_and_thread_time_limit=False, enable_per_process_and_thread_memory_limit=False,
              redirect_stderr_to_stdout=False, number_of_runs=1)

base = sys.argv[1].rstrip("/") if len(sys.argv) > 1 else sys.exit(__doc__)
lang = int(sys.argv[2]) if len(sys.argv) > 2 else 71
token = os.environ.get("CODE_RUNNER_TOKEN") or getpass.getpass("AUTHN_TOKEN исполнителя: ")


def call(method, path, body=None):
    req = urllib.request.Request(base + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", "X-Auth-Token": token})
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.loads(r.read() or b"null")


def b64(s):
    return base64.b64encode(s.encode()).decode()


def ub64(s):
    return base64.b64decode(s).decode("utf-8", "replace") if s else ""


def run(src, stdin=""):
    tok = call("POST", "/submissions?base64_encoded=true&wait=false", dict(LIMITS, source_code=b64(src), language_id=lang, stdin=b64(stdin)))["token"]
    for _ in range(120):
        time.sleep(0.5)
        r = call("GET", f"/submissions/{tok}?base64_encoded=true&fields=stdout,stderr,compile_output,message,status,time,memory,exit_code")
        if r["status"]["id"] not in (1, 2):
            return r["status"]["id"], ub64(r.get("stdout")), ub64(r.get("stderr")), r
    raise TimeoutError("нет результата за 60 с")


failed = 0


def check(name, ok, detail=""):
    global failed
    print(("  ✔ " if ok else "  ✘ ") + name + ("" if ok else f" — {detail}"))
    failed += 0 if ok else 1


print(f"Исполнитель: {base}, language_id={lang}")
try:
    about = call("GET", "/about")
    check(f"версия {about.get('version')} ≥ 1.13.1 (исправлены побеги из песочницы CVE-2024-28185/28189/29021)",
          tuple(int(x) for x in str(about.get("version", "0")).split(".")[:3]) >= (1, 13, 1), str(about))
    cfg = call("GET", "/config_info")
    check("сеть для программ запрещена (enable_network=false, allow_enable_network=false)",
          cfg.get("enable_network") is False and cfg.get("allow_enable_network") is False, str({k: cfg.get(k) for k in ("enable_network", "allow_enable_network")}))
    check("ожидание результата в запросе выключено (enable_wait_result=false)", cfg.get("enable_wait_result") is False, str(cfg.get("enable_wait_result")))
    lng = call("GET", f"/languages/{lang}")
    check(f"язык {lang}: {lng.get('name')}", "Python (3" in str(lng.get("name")), str(lng))
except urllib.error.HTTPError as e:
    sys.exit(f"API вернуло HTTP {e.code} — проверьте ключ, ALLOWED_IP и адрес")

s, out, err, _ = run('print("ok")')
check("print → Accepted, stdout «ok»", s == 3 and out == "ok\n", f"status={s} out={out!r}")
s, out, err, _ = run("print(input()[::-1])\nprint(int(input()) + int(input()))", "abc\n2\n3\n")
check("stdin: input() читает входные данные", s == 3 and out == "cba\n5\n", f"status={s} out={out!r}")
s, out, err, _ = run("print(")
check("синтаксическая ошибка → Runtime Error (NZEC), SyntaxError в stderr", s == 11 and "SyntaxError" in err, f"status={s} err={err[-200:]!r}")
t0 = time.time()
s, out, err, r = run("while True:\n    pass")
check("бесконечный цикл → Time Limit Exceeded (5)", s == 5, f"status={s} time={r.get('time')}")
s, out, err, _ = run('print("x" * 10_000_000)')
check("вывод 10 МБ при max_file_size=64 КБ → Runtime Error (SIGXFSZ) (8)", s == 8 and len(out) <= 70 * 1024, f"status={s} len={len(out)}")
s, out, err, r = run("a = bytearray(600 * 1024 * 1024)\nprint('allocated')")
check("выделение 600 МБ при лимите 128 МБ не удаётся", s != 3 and "allocated" not in out, f"status={s} out={out!r} mem={r.get('memory')}")
s, out, err, _ = run(
    "import os\nn = 0\ntry:\n    for _ in range(200):\n        if os.fork() == 0:\n            import time; time.sleep(3); os._exit(0)\n        n += 1\nexcept OSError as e:\n    print('LIMIT', n)\nelse:\n    print('NOLIMIT', n)")
check("число процессов ограничено (fork упирается в лимит)", "LIMIT" in out and "NOLIMIT" not in out, f"status={s} out={out!r}")
s, out, err, _ = run(
    "import socket\nfor host, port in [('1.1.1.1', 53), ('kubernetes.default.svc', 443), ('194.31.55.106', 443)]:\n"
    "    try:\n        socket.create_connection((host, port), timeout=2).close(); print('OPEN', host)\n"
    "    except Exception as e:\n        print('BLOCKED', host, type(e).__name__)")
check("нет доступа в сеть (интернет, Kubernetes API, сервер приложения)", "OPEN" not in out and out.count("BLOCKED") == 3, out)
s, out, err, _ = run("import os\nprint(sorted(os.environ))")
bad = [k for k in ("POSTGRES", "REDIS", "AUTHN", "AUTHZ", "PASSWORD", "TOKEN", "SECRET", "KUBERNETES") if k in out.upper()]
check("в окружении программы нет секретов и переменных Kubernetes", s == 3 and "PATH" in out and not bad, f"найдено: {bad}; {out[:300]!r}")
s, out, err, _ = run(
    "import os\nfor p in ['/judge0.conf', '/api/judge0.conf', '/var/run/secrets/kubernetes.io', '/root']:\n"
    "    try:\n        open(p).read(1) if os.path.isfile(p) else os.listdir(p); print('READ', p)\n"
    "    except Exception as e:\n        print('NO', p, type(e).__name__)")
check("файлы исполнителя и хоста недоступны", "READ" not in out and out.count("NO ") == 4, repr(out))

print("\nВсе проверки пройдены" if not failed else f"\nНе пройдено: {failed}")
sys.exit(1 if failed else 0)
