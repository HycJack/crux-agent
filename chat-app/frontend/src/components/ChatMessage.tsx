import { useState } from 'react';
import { Bot, CheckOutlined, CloseOutlined, CopyOutlined, UserOutlined } from '../icons';
import MarkdownRenderer from './MarkdownRenderer';
import ThinkingBlock from './ThinkingBlock';
import ToolCallBlock from './ToolCallBlock';
import type { ToolCall, ToolExecution } from '../types';

interface ChatMessageProps {
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
  isLoading?: boolean;
  thinking?: string;
  toolCalls?: ToolCall[];
  toolExecutions?: ToolExecution[];
  onSpeak?: () => void;
  onStopSpeak?: () => void;
  isSpeaking?: boolean;
}

export default function ChatMessage({
  role,
  content,
  timestamp,
  isLoading,
  thinking,
  toolCalls,
  toolExecutions,
  onSpeak,
  onStopSpeak,
  isSpeaking,
}: ChatMessageProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content);
    } catch {
      /* clipboard might be blocked */
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  if (role === 'user') {
    return (
      <div className="msg-row msg-row-user">
        <div className="msg-stack">
          <div className="msg-meta">
            <span className="msg-author">You</span>
            <span className="msg-time">{timestamp}</span>
          </div>
          <div className="msg-bubble user">
            <MarkdownRenderer content={content} />
          </div>
          <div className="msg-actions">
            <button className="ghost-action" onClick={handleCopy}>
              {copied ? <CheckOutlined size={12} /> : <CopyOutlined size={12} />}
              <span>{copied ? 'Copied' : 'Copy'}</span>
            </button>
          </div>
        </div>
        <div className="msg-avatar user">
          <UserOutlined size={16} />
        </div>
      </div>
    );
  }

  return (
    <div className="msg-row msg-row-assistant">
      <div className="msg-avatar assistant">
        <Bot size={18} />
      </div>
      <div className="msg-stack">
        <div className="msg-meta">
          <span className="msg-author">Crux Agent</span>
          <span className="msg-time">{timestamp || (isLoading ? 'generating…' : '')}</span>
        </div>
        <div className="msg-bubble assistant">
          <ThinkingBlock content={thinking || ''} />
          <ToolCallBlock toolCalls={toolCalls || []} toolExecutions={toolExecutions} />
          {content ? (
            <MarkdownRenderer content={content} />
          ) : (
            isLoading &&
            !thinking &&
            (!toolCalls || toolCalls.length === 0) && (
              <div className="generating-row">
                <span className="status-spinner" />
                <span>Thinking…</span>
              </div>
            )
          )}
        </div>
        {!isLoading && (content || toolCalls) && (
          <div className="msg-actions">
            <button className="ghost-action" onClick={handleCopy}>
              {copied ? <CheckOutlined size={12} /> : <CopyOutlined size={12} />}
              <span>{copied ? 'Copied' : 'Copy'}</span>
            </button>
            {content && (onSpeak || onStopSpeak) && (
              <button className={`ghost-action ${isSpeaking ? 'is-active' : ''}`} onClick={isSpeaking ? onStopSpeak : onSpeak}>
                <span className="status-dot" style={{ background: isSpeaking ? 'var(--n-error)' : 'var(--n-primary)' }} />
                <span>{isSpeaking ? 'Stop' : 'Speak'}</span>
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}