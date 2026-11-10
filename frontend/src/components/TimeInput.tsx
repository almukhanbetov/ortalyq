"use client";

import { maskHm, padHm } from "@/lib/time";

/**
 * Поле времени ЧЧ:ММ в 24-часовом формате с маской ввода.
 * Обычное текстовое поле (не type="time"), поэтому AM/PM не появляется ни в каком браузере и языке ОС.
 * На телефоне открывается цифровая клавиатура; двоеточие подставляется автоматически.
 * Проверку значения выполняет форма (см. hmProblem в lib/time).
 */
export function TimeInput({
  id,
  value,
  onChange,
  onBlur,
  disabled,
  invalid,
  describedBy,
  className = "",
  ariaLabel,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  /** После потери фокуса, со значением, дополненным до ЧЧ:ММ. */
  onBlur?: (v: string) => void;
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  className?: string;
  ariaLabel?: string;
}) {
  return (
    <input
      id={id}
      type="text"
      inputMode="numeric"
      autoComplete="off"
      placeholder="ЧЧ:ММ"
      maxLength={5}
      className={`input font-mono tabular-nums ${invalid ? "border-red-400 focus:border-red-500" : ""} ${className}`}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(maskHm(e.target.value))}
      onBlur={() => {
        const v = padHm(value);
        if (v !== value) onChange(v);
        onBlur?.(v);
      }}
      aria-label={ariaLabel}
      aria-describedby={describedBy}
      aria-invalid={invalid || undefined}
    />
  );
}
