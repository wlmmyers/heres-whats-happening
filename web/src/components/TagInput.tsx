import { useState, type KeyboardEvent } from 'react';
import * as s from './TagInput.css';

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
    <div className={s.wrapper}>
      {values.map((v) => (
        <span key={v} className={s.tag}>
          {v}
          <button
            type="button"
            aria-label={`Remove ${v}`}
            onClick={() => onRemove(v)}
            className={s.removeButton}
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
        className={s.input}
      />
    </div>
  );
}
