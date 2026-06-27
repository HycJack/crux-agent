import { useEffect, useRef } from 'react';
import { Bot, CodeOutlined, WrenchOutlined } from '../icons';
import ChatInput from './ChatInput';
import ChatMessage from './ChatMessage';
import type { Message } from '../types';

interface ChatAreaProps {
  messages: Message[];
  isLoading: boolean;
  workingDir: string;
  onSendMessage: (message: string) => void;
  onSpeak: (text: string, messageId: string) => void;
  onStopSpeak: () => void;
  speakingMessageId: string | null;
}

const suggestions = [
  'List the files in this workspace',
  'Read README.md and summarise it',
  'Run the project tests',
  'Find and fix TODO comments',
  'Create a hello.txt file',
  'Search for "TODO" across all .go files',
];

export default function ChatArea({
  messages,
  isLoading,
  workingDir,
  onSendMessage,
  onSpeak,
  onStopSpeak,
  speakingMessageId,
}: ChatAreaProps) {
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, isLoading]);

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
              <button key={title} className="suggestion-card" onClick={() => onSendMessage(title)}>
                <span className="suggestion-icon">
                  {title.toLowerCase().includes('run') ? <CodeOutlined /> : <WrenchOutlined />}
                </span>
                <span className="suggestion-text">{title}</span>
              </button>
            ))}
          </div>
        </div>
        <ChatInput onSend={onSendMessage} disabled={isLoading} placeholder="Ask Crux Agent…" />
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
      </div>
      <ChatInput onSend={onSendMessage} disabled={isLoading} placeholder="Ask Crux Agent…" />
    </section>
  );
}