import { useCallback, useEffect, useRef, useState } from 'react';
import { CancelStream, GetWorkingDir, StreamMessage } from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import ChatArea from './components/ChatArea';
import SettingsPanel from './components/SettingsPanel';
import Sidebar from './components/Sidebar';
import type { Conversation, Message, Settings, ToolExecution } from './types';

const SETTINGS_KEY = 'crux-agent-settings';

const defaultSettings: Settings = {
  provider: 'openai',
  apiKey: '',
  baseUrl: 'https://api.openai.com/v1',
  model: '',
  customModel: '',
  workingDir: '',
  ttsEnabled: false,
  ttsVoice: 'zh-CN',
};

function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    if (raw) {
      return { ...defaultSettings, ...JSON.parse(raw) };
    }
  } catch (e) {
    console.error('Failed to load settings:', e);
  }
  return defaultSettings;
}

function newConversation(): Conversation {
  return {
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: 'New chat',
    messages: [],
    timestamp: new Date().toLocaleDateString(),
  };
}

function formatTimestamp(date: Date) {
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}

function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [settings, setSettings] = useState<Settings>(loadSettings);
  const [speakingMessageId, setSpeakingMessageId] = useState<string | null>(null);

  const speechSynthesisRef = useRef<SpeechSynthesis | null>(null);
  const currentUtteranceRef = useRef<SpeechSynthesisUtterance | null>(null);
  const activeIdRef = useRef<string | null>(null);

  const speakText = useCallback(
    (text: string, messageId: string) => {
      if (!window.speechSynthesis) return;
      window.speechSynthesis.cancel();
      setSpeakingMessageId(messageId);

      const utterance = new SpeechSynthesisUtterance(text);
      utterance.lang = settings.ttsVoice;
      utterance.rate = 0.9;
      utterance.onend = () => setSpeakingMessageId(null);
      utterance.onerror = () => setSpeakingMessageId(null);
      currentUtteranceRef.current = utterance;
      window.speechSynthesis.speak(utterance);
    },
    [settings.ttsVoice],
  );

  const stopSpeaking = useCallback(() => {
    if (window.speechSynthesis) window.speechSynthesis.cancel();
    setSpeakingMessageId(null);
  }, []);

  useEffect(() => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
  }, [settings]);

  // Initialize working dir from backend on startup.
  useEffect(() => {
    GetWorkingDir()
      .then((dir) => {
        if (dir && !settings.workingDir) {
          setSettings((prev) => ({ ...prev, workingDir: dir }));
        }
      })
      .catch(() => undefined);
  }, []);

  const activeConversation = conversations.find((c) => c.id === activeConversationId) ?? null;

  const updateActive = useCallback((updater: (conv: Conversation) => Conversation) => {
    const id = activeIdRef.current;
    if (!id) return;
    setConversations((prev) => prev.map((c) => (c.id === id ? updater(c) : c)));
  }, []);

  const createNewConversation = useCallback(() => {
    const conv = newConversation();
    setConversations((prev) => [conv, ...prev]);
    setActiveConversationId(conv.id);
    activeIdRef.current = conv.id;
  }, []);

  const selectConversation = useCallback((id: string) => {
    setActiveConversationId(id);
    activeIdRef.current = id;
  }, []);

  const deleteConversation = useCallback(
    (id: string) => {
      setConversations((prev) => prev.filter((c) => c.id !== id));
      if (activeConversationId === id) {
        const remaining = conversations.filter((c) => c.id !== id);
        const next = remaining[0] ?? null;
        setActiveConversationId(next ? next.id : null);
        activeIdRef.current = next ? next.id : null;
      }
    },
    [activeConversationId, conversations],
  );

  // Streaming event handlers — keyed off the *active* conversation at send time.
  const handleStreamTextDelta = useCallback(
    (delta: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant') {
          messages[messages.length - 1] = { ...last, content: last.content + delta };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamThinkingDelta = useCallback(
    (delta: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant') {
          messages[messages.length - 1] = {
            ...last,
            thinking: (last.thinking || '') + delta,
          };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamToolCallStart = useCallback(
    (data: string) => {
      try {
        const toolCall = JSON.parse(data) as { id: string; name: string };
        updateActive((conv) => {
          const messages = [...conv.messages];
          const last = messages[messages.length - 1];
          if (last && last.role === 'assistant') {
            messages[messages.length - 1] = {
              ...last,
              toolCalls: [
                ...(last.toolCalls || []),
                { id: toolCall.id, name: toolCall.name, arguments: '' },
              ],
            };
          }
          return { ...conv, messages };
        });
      } catch (e) {
        console.error('Error parsing tool call start:', e);
      }
    },
    [updateActive],
  );

  const handleStreamToolCallDelta = useCallback(
    (delta: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && last.toolCalls && last.toolCalls.length > 0) {
          const toolCalls = [...last.toolCalls];
          const lastToolCall = { ...toolCalls[toolCalls.length - 1] };
          lastToolCall.arguments += delta;
          toolCalls[toolCalls.length - 1] = lastToolCall;
          messages[messages.length - 1] = { ...last, toolCalls };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamToolCallEnd = useCallback(
    (args: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && last.toolCalls && last.toolCalls.length > 0) {
          const toolCalls = [...last.toolCalls];
          const lastToolCall = { ...toolCalls[toolCalls.length - 1] };
          lastToolCall.arguments = args;
          toolCalls[toolCalls.length - 1] = lastToolCall;
          messages[messages.length - 1] = { ...last, toolCalls };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamToolExecStart = useCallback(
    (data: string) => {
      try {
        const info = JSON.parse(data) as { id: string; name: string };
        updateActive((conv) => {
          const messages = [...conv.messages];
          const last = messages[messages.length - 1];
          if (last && last.role === 'assistant') {
            const executions: ToolExecution[] = [
              ...(last.toolExecutions || []),
              { id: info.id, name: info.name },
            ];
            messages[messages.length - 1] = { ...last, toolExecutions: executions };
          }
          return { ...conv, messages };
        });
      } catch (e) {
        console.error('Error parsing tool exec start:', e);
      }
    },
    [updateActive],
  );

  const handleStreamToolExecEnd = useCallback(
    (result: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && last.toolExecutions && last.toolExecutions.length > 0) {
          const executions = [...last.toolExecutions];
          const lastExec = { ...executions[executions.length - 1] };
          lastExec.result = result;
          lastExec.isError = false;
          executions[executions.length - 1] = lastExec;
          messages[messages.length - 1] = { ...last, toolExecutions: executions };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamToolExecError = useCallback(
    (result: string) => {
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && last.toolExecutions && last.toolExecutions.length > 0) {
          const executions = [...last.toolExecutions];
          const lastExec = { ...executions[executions.length - 1] };
          lastExec.result = result;
          lastExec.isError = true;
          executions[executions.length - 1] = lastExec;
          messages[messages.length - 1] = { ...last, toolExecutions: executions };
        }
        return { ...conv, messages };
      });
    },
    [updateActive],
  );

  const handleStreamDone = useCallback(() => {
    updateActive((conv) => {
      const messages = [...conv.messages];
      const last = messages[messages.length - 1];
      if (last && last.role === 'assistant' && !last.timestamp) {
        messages[messages.length - 1] = { ...last, timestamp: formatTimestamp(new Date()) };
      }
      return { ...conv, messages };
    });
    setIsLoading(false);
  }, [updateActive]);

  const handleStreamError = useCallback(
    (error: string) => {
      console.error('Stream error:', error);
      updateActive((conv) => {
        const messages = [...conv.messages];
        const last = messages[messages.length - 1];
        if (last && last.role === 'assistant' && !last.content) {
          messages[messages.length - 1] = {
            ...last,
            content: error,
            timestamp: formatTimestamp(new Date()),
          };
        }
        return { ...conv, messages };
      });
      setIsLoading(false);
    },
    [updateActive],
  );

  useEffect(() => {
    EventsOn('stream-thinking-delta', handleStreamThinkingDelta);
    EventsOn('stream-tool-call-start', handleStreamToolCallStart);
    EventsOn('stream-tool-call-delta', handleStreamToolCallDelta);
    EventsOn('stream-tool-call-end', handleStreamToolCallEnd);
    EventsOn('stream-text-delta', handleStreamTextDelta);
    EventsOn('stream-tool-exec-start', handleStreamToolExecStart);
    EventsOn('stream-tool-exec-end', handleStreamToolExecEnd);
    EventsOn('stream-tool-exec-error', handleStreamToolExecError);
    EventsOn('stream-done', handleStreamDone);
    EventsOn('stream-error', handleStreamError);

    return () => {
      EventsOff('stream-thinking-delta');
      EventsOff('stream-tool-call-start');
      EventsOff('stream-tool-call-delta');
      EventsOff('stream-tool-call-end');
      EventsOff('stream-text-delta');
      EventsOff('stream-tool-exec-start');
      EventsOff('stream-tool-exec-end');
      EventsOff('stream-tool-exec-error');
      EventsOff('stream-done');
      EventsOff('stream-error');
    };
  }, [
    handleStreamThinkingDelta,
    handleStreamToolCallStart,
    handleStreamToolCallDelta,
    handleStreamToolCallEnd,
    handleStreamTextDelta,
    handleStreamToolExecStart,
    handleStreamToolExecEnd,
    handleStreamToolExecError,
    handleStreamDone,
    handleStreamError,
  ]);

  const handleSendMessage = useCallback(
    async (message: string) => {
      stopSpeaking();
      setIsLoading(true);

      let targetId = activeIdRef.current;
      if (!targetId) {
        const conv = newConversation();
        setConversations((prev) => [conv, ...prev]);
        setActiveConversationId(conv.id);
        activeIdRef.current = conv.id;
        targetId = conv.id;
      }

      const userMessage: Message = {
        id: `user-${Date.now()}`,
        role: 'user',
        content: message,
        timestamp: formatTimestamp(new Date()),
      };
      const assistantMessage: Message = {
        id: `assistant-${Date.now()}`,
        role: 'assistant',
        content: '',
        timestamp: '',
      };

      setConversations((prev) =>
        prev.map((c) => {
          if (c.id !== targetId) return c;
          const isFirst = c.messages.length === 0;
          return {
            ...c,
            title: isFirst ? message.slice(0, 30) + (message.length > 30 ? '…' : '') : c.title,
            timestamp: new Date().toLocaleDateString(),
            messages: [...c.messages, userMessage, assistantMessage],
          };
        }),
      );

      try {
        await StreamMessage({
          message,
          provider: settings.provider,
          apiKey: settings.apiKey,
          baseUrl: settings.baseUrl,
          model: settings.model === 'custom' ? settings.customModel : settings.model,
        });
      } catch (error) {
        console.error('StreamMessage failed:', error);
        setConversations((prev) =>
          prev.map((c) => {
            if (c.id !== targetId) return c;
            const messages = [...c.messages];
            const last = messages[messages.length - 1];
            if (last && last.role === 'assistant' && !last.content) {
              messages[messages.length - 1] = {
                ...last,
                content: 'Sorry, something went wrong. Please try again.',
                timestamp: formatTimestamp(new Date()),
              };
            }
            return { ...c, messages };
          }),
        );
        setIsLoading(false);
      }
    },
    [settings, stopSpeaking],
  );

  const handleSaveSettings = useCallback((newSettings: Settings) => {
    setSettings(newSettings);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setIsSettingsOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <div className="app-shell">
      <Sidebar
        conversations={conversations}
        activeConversation={activeConversationId}
        workingDir={settings.workingDir}
        onSelectConversation={selectConversation}
        onCreateNewConversation={createNewConversation}
        onDeleteConversation={deleteConversation}
        onOpenSettings={() => setIsSettingsOpen(true)}
      />

      <main className="main-frame">
        <header className="topbar">
          <div className="breadcrumb">
            <span className="crumb-root">Crux Agent</span>
            <span className="crumb-sep">/</span>
            <strong className="crumb-current">
              {activeConversation ? activeConversation.title : 'Workspace'}
            </strong>
          </div>
          <div className="topbar-right">
            <div className={`bridge-status ${isLoading ? 'busy' : 'idle'}`}>
              <span className={isLoading ? 'status-spinner' : 'status-dot green'} />
              <span>{isLoading ? 'Agent working…' : 'Ready'}</span>
            </div>
            {isLoading && (
              <button className="btn-danger" onClick={() => CancelStream().catch(() => undefined)}>
                Stop
              </button>
            )}
          </div>
        </header>

        <ChatArea
          messages={activeConversation?.messages ?? []}
          isLoading={isLoading}
          onSendMessage={handleSendMessage}
          onSpeak={speakText}
          onStopSpeak={stopSpeaking}
          speakingMessageId={speakingMessageId}
          workingDir={settings.workingDir}
        />
      </main>

      <SettingsPanel
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        currentSettings={settings}
        onSave={handleSaveSettings}
      />
    </div>
  );
}

export default App;