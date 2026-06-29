import { useCallback, useEffect, useRef, useState } from 'react';
import { Bot, ChevronDownOutlined, CodeOutlined, WrenchOutlined } from '../icons';
import ChatInput from './ChatInput';
import ChatMessage from './ChatMessage';
import type { Message } from '../types';

interface ModelInfo {
  id: string;
  name: string;
  reasoning?: boolean;
  thinkingLevelMap?: Record<string, string>;
}

interface ChatAreaProps {
  messages: Message[];
  isLoading: boolean;
  workingDir: string;
  onSendMessage: (message: string, model?: string, thinkingLevel?: string) => void;
  onStop: () => void;
  onSpeak: (text: string, messageId: string) => void;
  onStopSpeak: () => void;
  speakingMessageId: string | null;
  models?: ModelInfo[];
  currentModel?: string;
  currentThinkingLevel?: string;
  onModelChange?: (model: string) => void;
  onThinkingLevelChange?: (level: string) => void;
}

const suggestions = [
  'List the files in this workspace',
  'Read README.md and summarise it',
  'Run the project tests',
  'Find and fix TODO comments',
  'Create a hello.txt file',
  'Search for "TODO" across all .go files',
];

// Distance in pixels from the bottom within which we still consider the
// user to be "following" the conversation and auto-scroll on new content.
const NEAR_BOTTOM_PX = 120;

// scrollToBottom forces an immediate scroll regardless of user position.
// Used after the user sends a message so they land on the new content.
function scrollToBottom(container: HTMLDivElement | null, end: HTMLDivElement | null) {
  if (!container || !end) return;
  container.scrollTop = container.scrollHeight;
  end.scrollIntoView({ block: 'end' });
}

export default function ChatArea({
  messages,
  isLoading,
  workingDir,
  onSendMessage,
  onStop,
  onSpeak,
  onStopSpeak,
  speakingMessageId,
  models,
  currentModel,
  currentThinkingLevel,
  onModelChange,
  onThinkingLevelChange,
}: ChatAreaProps) {
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // True when the viewport is close enough to the bottom that we should
  // keep following incoming tokens. False once the user scrolls up to
  // read earlier content.
  const [stuckToBottom, setStuckToBottom] = useState(true);
  // Number of new tokens that arrived while the user was scrolled away.
  // Surfaced as a "N new messages" pill so they can jump back easily.
  const [pendingCount, setPendingCount] = useState(0);
  // Bumps on each render where messages change so the scroll effect can
  // detect deltas without needing a callback into App.tsx.
  const lastMsgCountRef = useRef(0);

  // Sync "near bottom" state from a scroll listener.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const onScroll = () => {
      const distanceFromBottom =
        el.scrollHeight - el.scrollTop - el.clientHeight;
      const near = distanceFromBottom < NEAR_BOTTOM_PX;
      setStuckToBottom(near);
      if (near) setPendingCount(0);
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  // Reset stickiness whenever the conversation changes (different message
  // list — usually because the user switched conversations). We key off
  // the *first* message id so a streaming update on the current
  // conversation does not retrigger this effect.
  const conversationKey = messages[0]?.id ?? null;
  useEffect(() => {
    lastMsgCountRef.current = messages.length;
    setPendingCount(0);
    setStuckToBottom(true);
    // Force-scroll to bottom on conversation switch.
    requestAnimationFrame(() => {
      scrollToBottom(containerRef.current, messagesEndRef.current);
    });
  }, [conversationKey]);

  // Auto-scroll on new content, but only when stuck to the bottom.
  useEffect(() => {
    const prevCount = lastMsgCountRef.current;
    const newCount = messages.length;
    lastMsgCountRef.current = newCount;
    if (newCount > prevCount) {
      // A new message bubble was appended (user send, or first delta).
      // Treat this as a sticky signal — the user just sent something, so
      // they want to see it.
      setStuckToBottom(true);
      requestAnimationFrame(() => {
        scrollToBottom(containerRef.current, messagesEndRef.current);
      });
      return;
    }
    // Same number of messages but content grew — that's a streaming delta.
    if (!stuckToBottom) {
      setPendingCount((c) => c + 1);
      return;
    }
    requestAnimationFrame(() => {
      const el = messagesEndRef.current;
      const container = containerRef.current;
      if (!el || !container) return;
      el.scrollIntoView({ behavior: 'smooth', block: 'end' });
    });
  }, [messages, stuckToBottom]);

  const jumpToBottom = useCallback(() => {
    setStuckToBottom(true);
    setPendingCount(0);
    requestAnimationFrame(() => {
      scrollToBottom(containerRef.current, messagesEndRef.current);
    });
  }, []);

  const handleLocalSend = useCallback(
    (message: string, modelOverride?: string, thinkingLevel?: string) => {
      onSendMessage(message, modelOverride, thinkingLevel);
      // Optimistically scroll so the user sees their message + the
      // assistant's reply even before any event fires.
      requestAnimationFrame(() => {
        scrollToBottom(containerRef.current, messagesEndRef.current);
      });
    },
    [onSendMessage],
  );

  if (messages.length === 0) {
    const dirLabel = workingDir ? workingDir : 'No workspace selected';
    return (
      <section className="stage stage-empty">
        <div className="stage-inner">
          <div className="hero-mark">
            <Bot size={32} />
          </div>
          <h1 className="hero-title">Hi, I'm Crux Agent.</h1>
          <p className="hero-subtitle">
            I can read, write and run commands in <strong className="hero-path">{dirLabel}</strong>.
            Ask me to explore the codebase, refactor a file, run tests, or scaffold something new.
          </p>

          <div className="suggestion-grid">
            {suggestions.map((title) => (
              <button key={title} className="suggestion-card" onClick={() => handleLocalSend(title)}>
                <span className="suggestion-icon">
                  {title.toLowerCase().includes('run') ? <CodeOutlined size={16} /> : <WrenchOutlined size={16} />}
                </span>
                <span className="suggestion-text">{title}</span>
              </button>
            ))}
          </div>
        </div>
        <ChatInput
          onSend={handleLocalSend}
          onStop={onStop}
          disabled={isLoading}
          placeholder="Ask Crux Agent…"
          models={models}
          currentModel={currentModel}
          currentThinkingLevel={currentThinkingLevel}
          onModelChange={onModelChange}
          onThinkingLevelChange={onThinkingLevelChange}
        />
      </section>
    );
  }

  const lastId = messages[messages.length - 1]?.id;

  return (
    <section className="stage">
      <div ref={containerRef} className="stage-scroll">
        <div className="stage-inner stage-inner-wide">
          {messages.map((msg) => (
            <ChatMessage
              key={msg.id}
              role={msg.role}
              content={msg.content}
              timestamp={msg.timestamp}
              isLoading={msg.role === 'assistant' && isLoading && msg.id === lastId}
              thinking={msg.thinking}
              toolCalls={msg.toolCalls}
              toolExecutions={msg.toolExecutions}
              onSpeak={
                msg.role === 'assistant' && msg.content && speakingMessageId !== msg.id
                  ? () => onSpeak(msg.content, msg.id)
                  : undefined
              }
              onStopSpeak={
                msg.role === 'assistant' && msg.content && speakingMessageId === msg.id ? onStopSpeak : undefined
              }
              isSpeaking={speakingMessageId === msg.id}
            />
          ))}
          <div ref={messagesEndRef} />
        </div>

        {!stuckToBottom && (
          <button className="scroll-to-bottom-pill" onClick={jumpToBottom} title="Jump to latest">
            <ChevronDownOutlined size={14} />
            <span>
              {pendingCount > 0
                ? `${pendingCount} new ${pendingCount === 1 ? 'update' : 'updates'}`
                : 'Jump to latest'}
            </span>
          </button>
        )}
      </div>
      <ChatInput
        onSend={handleLocalSend}
        onStop={onStop}
        disabled={isLoading}
        placeholder="Ask Crux Agent…"
        models={models}
        currentModel={currentModel}
        currentThinkingLevel={currentThinkingLevel}
        onModelChange={onModelChange}
        onThinkingLevelChange={onThinkingLevelChange}
      />
    </section>
  );
}