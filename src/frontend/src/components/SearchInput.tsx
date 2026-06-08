import {
  useState, useRef, useEffect, useCallback,
  type ChangeEvent, type KeyboardEvent,
} from "react";
import { useQuery } from "@tanstack/react-query";

async function fetchTracked(): Promise<string[]> {
  const res = await fetch("/admin/tracked");
  if (!res.ok) return [];
  const data = await res.json() as string[] | null;
  return data ?? [];
}

interface Props {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  placeholder?: string;
}

export function SearchInput({ value, onChange, disabled, placeholder }: Props) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);     // keyboard-highlighted index
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef  = useRef<HTMLUListElement>(null);
  const wrapRef  = useRef<HTMLDivElement>(null);

  // Fetch tracked namespace/service pairs from the backend, refresh every 15s
  const { data: all = [] } = useQuery<string[]>({
    queryKey: ["tracked"],
    queryFn: fetchTracked,
    refetchInterval: 15_000,
  });

  // Filter: match any part of the suggestion (namespace OR service)
  const q = value.trim().toLowerCase();
  const suggestions = q.length === 0
    ? all                                         // show all when field is empty/focused
    : all.filter(s => s.toLowerCase().includes(q));

  // Close on outside click
  useEffect(() => {
    function handle(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
        setActive(-1);
      }
    }
    document.addEventListener("mousedown", handle);
    return () => document.removeEventListener("mousedown", handle);
  }, []);

  const pick = useCallback((s: string) => {
    onChange(s);
    setOpen(false);
    setActive(-1);
    inputRef.current?.focus();
  }, [onChange]);

  function onInput(e: ChangeEvent<HTMLInputElement>) {
    onChange(e.target.value);
    setOpen(true);
    setActive(-1);
  }

  function onFocus() {
    setOpen(true);
    setActive(-1);
  }

  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (!open || suggestions.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(i => Math.min(i + 1, suggestions.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(i => Math.max(i - 1, 0));
    } else if (e.key === "Enter" && active >= 0) {
      e.preventDefault();
      pick(suggestions[active]);
    } else if (e.key === "Escape") {
      setOpen(false);
      setActive(-1);
    }
  }

  // Scroll active item into view
  useEffect(() => {
    if (active >= 0 && listRef.current) {
      const item = listRef.current.children[active] as HTMLElement | undefined;
      item?.scrollIntoView({ block: "nearest" });
    }
  }, [active]);

  // Highlight the matching portion of a suggestion
  function highlight(s: string) {
    if (!q) return <>{s}</>;
    const idx = s.toLowerCase().indexOf(q);
    if (idx === -1) return <>{s}</>;
    return (
      <>
        {s.slice(0, idx)}
        <mark>{s.slice(idx, idx + q.length)}</mark>
        {s.slice(idx + q.length)}
      </>
    );
  }

  return (
    <div className="search-wrap" ref={wrapRef}>
      <span className="search-icon">⌕</span>
      <input
        ref={inputRef}
        className="search-input"
        type="text"
        value={value}
        onChange={onInput}
        onFocus={onFocus}
        onKeyDown={onKey}
        placeholder={placeholder ?? "namespace/service"}
        disabled={disabled}
        autoComplete="off"
        aria-autocomplete="list"
        aria-expanded={open && suggestions.length > 0}
        required
      />
      {open && suggestions.length > 0 && (
        <ul
          className="search-dropdown"
          ref={listRef}
          role="listbox"
        >
          {suggestions.map((s, i) => {
            const [ns, svc] = s.split("/");
            return (
              <li
                key={s}
                className={`search-option${i === active ? " search-option--active" : ""}`}
                onMouseDown={e => { e.preventDefault(); pick(s); }}
                onMouseEnter={() => setActive(i)}
                role="option"
                aria-selected={i === active}
              >
                <span className="search-option__ns">{ns}/</span>
                <span className="search-option__svc">{highlight(svc ?? "")}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
