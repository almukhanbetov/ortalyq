export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* не JSON */
  }
  if (!res.ok) {
    const serverMsg = data && typeof data === "object" && "error" in data ? String((data as { error: unknown }).error) : "";
    const msg =
      serverMsg ||
      (res.status === 502 || res.status === 504 || res.status === 500
        ? "Сервер недоступен или вернул ошибку. Попробуйте позже."
        : `Ошибка ${res.status}`);
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export async function api<T = unknown>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const { json, headers, ...rest } = init;
  let res: Response;
  try {
    res = await fetch(path, {
      credentials: "include",
      cache: "no-store",
      ...rest,
      headers: {
        ...(json !== undefined ? { "Content-Type": "application/json" } : {}),
        ...headers,
      },
      body: json !== undefined ? JSON.stringify(json) : rest.body,
    });
  } catch {
    throw new ApiError(0, "Нет соединения с сервером. Проверьте интернет и попробуйте снова.");
  }
  return parse<T>(res);
}

export function upload<T = unknown>(path: string, form: FormData): Promise<T> {
  return api<T>(path, { method: "POST", body: form });
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return "Что-то пошло не так";
}

// ---- Типы ----

export type Role = "student" | "teacher" | "admin";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  timezone: string;
  city?: string;
  phone?: string;
}

export interface Teacher {
  id: string;
  name: string;
  headline: string;
  bio: string;
  subjects: string[];
  price: number;
  experienceYears: number;
  photoUrl: string | null;
  rating: number | null;
  reviewCount: number;
  isDemo: boolean;
  timezone: string;
  city: string;
}

export interface Listing {
  id: string;
  authorId: string;
  authorName: string;
  authorRole: Role;
  kind: "offer" | "request";
  subject: string;
  title: string;
  body: string;
  price: number | null;
  hidden: boolean;
  hiddenReason?: string;
  isDemo: boolean;
  createdAt: string;
  updatedAt: string;
}

export type LessonStatus = "scheduled" | "live" | "completed" | "cancelled";

export interface Lesson {
  id: string;
  teacherId: string;
  teacherName: string;
  studentId: string;
  studentName: string;
  subject: string;
  note: string;
  startsAt: string;
  endsAt: string;
  status: LessonStatus;
  startedAt: string | null;
  endedAt: string | null;
  cancelReason: string;
  price: number;
  isDemo: boolean;
  hasTeacherPhoto: boolean;
  /** Демо-класс: вход открыт в любое время */
  alwaysOpen: boolean;
}

export type RecordingStatus =
  | "awaiting_consent"
  | "starting"
  | "recording"
  | "processing"
  | "ready"
  | "failed"
  | "declined";

export interface Recording {
  id: string;
  status: RecordingStatus;
  teacherConsentAt: string | null;
  studentConsentAt: string | null;
  sizeBytes: number | null;
  durationMs: number | null;
  error: string;
  startedAt: string | null;
  endedAt: string | null;
  createdAt: string;
}

export interface Material {
  id: string;
  filename: string;
  contentType: string;
  size: number;
  createdAt: string;
  uploadedBy: string;
}

export interface Submission {
  id: string;
  body: string;
  fileName: string | null;
  fileSize: number | null;
  status: "submitted" | "reviewed";
  teacherComment: string;
  grade: number | null;
  submittedAt: string;
  reviewedAt: string | null;
}

export interface Homework {
  id: string;
  title: string;
  description: string;
  dueAt: string | null;
  fileName: string | null;
  fileSize: number | null;
  createdAt: string;
  submission: Submission | null;
}

export interface LessonDetail {
  lesson: Lesson;
  myRole: "teacher" | "student";
  canJoin: boolean;
  joinOpensAt: string;
  joinClosesAt: string;
  materials: Material[];
  homework: Homework[];
  recordings: Recording[];
  review: { rating: number; body: string; createdAt: string } | null;
  board: { revision: number; elementCount: number };
  chatCount: number;
}

export interface PublicConfig {
  videoConfigured: boolean;
  storageConfigured: boolean;
  recordingConfigured: boolean;
  recordingProblem: string;
  maxUploadMB: number;
  joinEarlyMinutes: number;
  codeRunnerConfigured: boolean;
}

export interface ChatMessage {
  id: number;
  userId: string;
  userName: string;
  body: string;
  createdAt: string;
  clientId?: string;
}

// ---- Редактор кода ----

export type CodeRunStatus =
  | "running"
  | "completed"
  | "runtime_error"
  | "compile_error"
  | "time_limit"
  | "memory_limit"
  | "output_limit"
  | "unavailable"
  | "not_configured"
  | "cancelled"
  | "internal_error";

export interface CodeRun {
  id: string;
  userId: string;
  userName: string;
  language: string;
  status: CodeRunStatus;
  stdout: string;
  stderr: string;
  message: string;
  exitCode: number | null;
  timeMs: number | null;
  memoryKb: number | null;
  truncated: boolean;
  sourceBytes: number;
  stdinBytes: number;
  createdAt: string;
  finishedAt: string | null;
}

export interface CodeLanguage {
  id: string;
  label: string;
  monaco: string;
}

export interface CodeInfo {
  languages: CodeLanguage[];
  runnerConfigured: boolean;
  lastRun: CodeRun | null;
  limits: { sourceBytes: number; stdinBytes: number; outputBytes: number; cpuSeconds: number; wallSeconds: number; memoryMb: number };
}
