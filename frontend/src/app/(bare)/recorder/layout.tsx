import type { Metadata } from "next";

// Отключаем автоперевод Chrome в шаблоне записи (иначе в кадр попадает панель переводчика).
export const metadata: Metadata = {
  title: "ORTALYQ — запись занятия",
  other: { google: "notranslate" },
  robots: { index: false, follow: false },
};

export default function RecorderLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
