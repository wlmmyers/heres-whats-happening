import { useState, type KeyboardEvent } from 'react';

interface Props {
  values: string[];
  onAdd: (value: string) => void;
  onRemove: (value: string) => void;
  placeholder?: string;
}

export default function TagInput({ values, onAdd, onRemove, placeholder }: Props) {
  const [text, setText] = useState('');

  function commit() {
    const v = text.trim();
    if (v === '') return;
    onAdd(v);
    setText('');
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault();
      commit();
    }
  }

  return (
    <div className="border rounded p-2 flex flex-wrap gap-2 items-center">
      {values.map((v) => (
        <span key={v} className="inline-flex items-center bg-blue-100 text-blue-800 rounded-full px-3 py-1 text-sm">
          {v}
          <button
            type="button"
            aria-label={`Remove ${v}`}
            onClick={() => onRemove(v)}
            className="ml-2 text-blue-700 hover:text-red-600"
          >
            ×
          </button>
        </span>
      ))}
      <input
        type="text"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder={placeholder}
        className="flex-1 min-w-[120px] border-0 outline-none p-1 text-sm"
      />
    </div>
  );
}
