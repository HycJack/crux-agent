import { KeyboardEvent, useMemo, useState } from 'react';
import { SendOutlined, StopOutlined, Settings2, Brain } from '../icons';

interface ModelInfo {
  id: string;
  name: string;
  reasoning?: boolean;
  thinkingLevelMap?: Record<string, string>;
}

interface ChatInputProps {
  onSend: (message: string, model?: string, thinkingLevel?: string) => void;
  disabled?: boolean;
  placeholder?: string;
  models?: ModelInfo[];
  currentModel?: string;
  currentThinkingLevel?: string;
  onModelChange?: (model: string) => void;
  onThinkingLevelChange?: (level: string) => void;
}

const THINKING_LEVELS = [
  { value: '', label: 'Off' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
];

export default function ChatInput({
  onSend,
  disabled,
  placeholder,
  models,
  currentModel,
  currentThinkingLevel = '',
  onModelChange,
  onThinkingLevelChange,
}: ChatInputProps) {
  const [value, setValue] = useState('');
  const [showModelPicker, setShowModelPicker] = useState(false);

  const submit = () => {
    const text = value.trim();
    if (!text || disabled) return;
    setValue('');
    onSend(text, currentModel, currentThinkingLevel);
  };

  const handleKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  const modelLabel = models?.find((m) => m.id === currentModel)?.name || currentModel || 'Select model';

  // Determine whether the current model supports reasoning
  const currentModelInfo = models?.find((m) => m.id === currentModel);
  const supportsReasoning = currentModelInfo?.reasoning;

  // Get available thinking levels from the model's thinkingLevelMap
  const availableLevels = useMemo(() => {
    if (!currentModelInfo?.thinkingLevelMap) return THINKING_LEVELS;
    const map = currentModelInfo.thinkingLevelMap;
    // Filter to only levels present in the map (plus "Off")
    return THINKING_LEVELS.filter((l) => l.value === '' || map[l.value]);
  }, [currentModelInfo]);

  const showThinking = supportsReasoning && onThinkingLevelChange && availableLevels.length > 1;

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

        {/* Model & thinking toolbar */}
        <div className="composer-toolbar">
          <div className="composer-toolbar-left">
            {/* Model selector */}
            {models && models.length > 0 && (
              <div className="model-picker-wrap">
                <button
                  type="button"
                  className="model-picker-btn"
                  onClick={() => setShowModelPicker(!showModelPicker)}
                  title={`Current model: ${modelLabel}`}
                >
                  <Settings2 size={13} />
                  <span>{modelLabel}</span>
                </button>
                {showModelPicker && (
                  <>
                    <div className="model-picker-overlay" onClick={() => setShowModelPicker(false)} />
                    <div className="model-picker-dropdown">
                      <div className="model-picker-header">Switch model</div>
                      {models.map((m) => (
                        <button
                          key={m.id}
                          className={`model-picker-item ${currentModel === m.id ? 'active' : ''}`}
                          onClick={() => {
                            onModelChange?.(m.id);
                            setShowModelPicker(false);
                          }}
                        >
                          <span className="model-picker-name">{m.name}</span>
                          <span className="model-picker-id">{m.id}</span>
                        </button>
                      ))}
                    </div>
                  </>
                )}
              </div>
            )}

            {/* Thinking level selector */}
            {showThinking && (
              <div className="thinking-control" title="Thinking depth">
                <Brain size={13} />
                <div className="thinking-levels">
                  {availableLevels.map((lvl) => (
                    <button
                      key={lvl.value}
                      type="button"
                      className={`thinking-level-btn ${currentThinkingLevel === lvl.value ? 'active' : ''}`}
                      onClick={() => onThinkingLevelChange?.(lvl.value)}
                    >
                      {lvl.label}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>

          <div className="composer-toolbar-right">
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
    </div>
  );
}
