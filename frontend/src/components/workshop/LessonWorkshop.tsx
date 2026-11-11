"use client";

// Раздел «Учебная мастерская» на странице урока: доступен участникам и после окончания урока.
// Ученик видит только опубликованные разборы и задания (сервер не отдаёт ему черновики и ответы).
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, WorkshopState } from "@/lib/api";
import { ErrorBox, Spinner } from "../ui";
import { WorkshopMaterials } from "./Materials";

export function LessonWorkshop({ lessonId, tz, cancelled }: { lessonId: string; tz: string; cancelled: boolean }) {
  const [state, setState] = useState<WorkshopState | null>(null);
  const [error, setError] = useState("");
  const [text, setText] = useState<string | null>(null);
  const [textBusy, setTextBusy] = useState(false);

  const load = useCallback(() => {
    api<WorkshopState>(`/api/lessons/${lessonId}/workshop`)
      .then((s) => {
        setState(s);
        setError("");
      })
      .catch((e) => setError(errorMessage(e)));
  }, [lessonId]);
  useEffect(load, [load]);

  const showText = async () => {
    setTextBusy(true);
    try {
      const r = await api<{ updates: string[] }>(`/api/lessons/${lessonId}/workshop/doc`);
      const { docFromUpdates } = await import("@/lib/yjsSync"); // Yjs — только по запросу
      const doc = docFromUpdates(r.updates);
      setText(doc.getText("text").toString());
      doc.destroy();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setTextBusy(false);
    }
  };

  return (
    <section className="card p-5" id="workshop" data-testid="lesson-workshop">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">Учебная мастерская</h2>
        {text === null && (
          <button className="btn-secondary btn-sm" onClick={showText} disabled={textBusy}>
            {textBusy && <Spinner className="h-3 w-3" />} Показать учебный текст
          </button>
        )}
      </div>
      {error && <ErrorBox message={error} onRetry={load} />}
      {text !== null && (
        <div className="mb-4">
          <div className="mb-1 text-xs font-semibold text-muted">Учебный текст урока</div>
          <pre className="max-h-80 overflow-y-auto whitespace-pre-wrap break-words rounded-xl bg-canvas px-3 py-2 font-sans text-sm" data-testid="lesson-workshop-text">
            {text || <span className="text-muted">Текст пуст</span>}
          </pre>
        </div>
      )}
      {!state ? (
        !error && <Spinner className="h-4 w-4" />
      ) : (
        <WorkshopMaterials
          lessonId={lessonId}
          state={state}
          setState={(fn) => setState((s) => (s ? fn(s) : s))}
          tz={tz}
          canModify={!cancelled}
          emptyText={state.role === "teacher" ? "Разборов и заданий пока нет. Они создаются во вкладке «Учебная мастерская» в классе." : "Преподаватель пока не опубликовал разборы и задания."}
        />
      )}
    </section>
  );
}
