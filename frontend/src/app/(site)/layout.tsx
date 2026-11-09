import { Header } from "@/components/Header";

export default function SiteLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <Header />
      <main className="mx-auto w-full max-w-6xl px-4 pb-16 pt-6 sm:px-6 sm:pt-8">{children}</main>
      <footer className="border-t border-line py-6 text-center text-xs text-muted">
        ORTALYQ · демонстрационная версия (MVP). Платежи не подключены. Данные с пометкой «ДЕМО» вымышлены.
      </footer>
    </>
  );
}
