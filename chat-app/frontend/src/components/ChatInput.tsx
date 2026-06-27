import { KeyboardEvent, useState } from 'react';
import { SendOutlined, StopOutlined } from '../icons';

interface ChatInputProps {
  onSend: (message: string) => void;
  disabled?: boolean;
  placeholder?: string;
}

export default function ChatInput({ onSend, disabled, placeholder }: ChatInputProps) {
  const [value, setValue] = useState('');

  const submit = () => {
    const text = value.trim();
    if (!text || disabled) return;
    setValue('');
    onSend(text);
  };

  const handleKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="composer">
      <div className={`composer-card ${disabled ? 'is-disabled' : ''}`}>
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKey}
          placeholder={placeholder ?? 'Ask Crux Agent…'}
          disabled={disabled}
          rows={1}
          className="composer-input"
        />
        <div className="composer-actions">
          <span className="composer-hint">Enter to send · Shift+Enter for newline</span>
          <button
            type="button"
            className={`send-btn ${disabled ? 'is-loading' : ''}`}
            onClick={submit}
            disabled={!value.trim() && !disabled}
            aria-label={disabled ? 'Stop' : 'Send'}
            title={disabled ? 'Generating…' : 'Send'}
          >
            {disabled ? <StopOutlined size={14} /> : <SendOutlined size={14} />}
          </button>
        </div>
      </div>
    </div>
  );
}