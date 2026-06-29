import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CancelStream,
  GetWorkingDir,
  LoadConversations,
  LoadSettings,
  SaveConversations,
  SaveSettings,
  StreamMessage,
} from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import ChatArea from './components/ChatArea';
import SettingsPanel from './components/SettingsPanel';
import Sidebar from './components/Sidebar';
import type { Conversation, Message, Settings, ToolExecution } from './types';
import { MenuOutlined } from './icons';

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

// PersistedSettings is the JSON shape coming back from the Go backend.
// `provider` arrives as a plain string; narrow it to the union the
// frontend actually expects so downstream code stays type-safe.
function settingsFromPersisted(p: Record<string, unknown>): Settings {
  const provider = p.provider === 'anthropic' ? 'anthropic' : 'openai';
  // Only override defaults with non-empty values from the persisted data,
  // so that a zero-value field (e.g. empty apiKey on a clean install)
  // does NOT overwrite a user-provided default.
  const out: Settings = { ...defaultSettings, provider };
  for (const [k, v] of Object.entries(p)) {
    if (v !== '' && v !== null && v !== undefined) {
      (out as any)[k] = v;
    }
  }
  return out;
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

// updateLastAssistant returns a new conversation with the last assistant
// message mutated via mutator. If the last message isn't from the assistant,
// the conversation is returned unchanged. Used by every stream handler —
// extracting it removes ~70 lines of repeated boilerplate.
function updateLastAssistant(
  conv: Conversation,
  mutator: (msg: Message) => Message,
): Conversation {
  const messages = conv.messages;
  const last = messages[messages.length - 1];
  if (!last || last.role !== 'assistant') return conv;
  const updated = mutator(last);
  if (updated === last) return conv;
  return {
    ...conv,
    messages: [...messages.slice(0, -1), updated],
  };
}

function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [settings, setSettings] = useState<Settings>(defaultSettings);
  const [speakingMessageId, setSpeakingMessageId] = useState<string | null>(null);
  const [hasLoaded, setHasLoaded] = useState(false);

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

  // On first mount: load settings + conversations from the OS-conventional
  // data dir (managed by the Go backend), then reconcile working dir from
  // the backend's authoritative state. `hasLoaded` gates the persist
  // effects below so we don't immediately write the defaults back over
  // what we just read.
  //
  // If LoadConversations returns an empty result on the first attempt,
  // retry once after a short delay — the Wails IPC or file system may
  // not be ready immediately on some Windows configurations.
  useEffect(() => {
    let cancelled = false;
    let attempts = 0;

    async function load() {
      attempts++;
      const [persisted, backendDir, persistedConvs] = await Promise.all([
        LoadSettings().catch(() => null),
        GetWorkingDir().catch(() => ''),
        LoadConversations().catch(() => null),
      ]);
      if (cancelled) return;

      if (persisted) {
        setSettings((prev) => ({ ...prev, ...settingsFromPersisted(persisted as unknown as Record<string, unknown>) }));
      }
      if (backendDir) {
        setSettings((prev) => ({ ...prev, workingDir: backendDir }));
      }
      if (persistedConvs && persistedConvs.length > 0) {
        setConversations(persistedConvs as unknown as Conversation[]);
        const lastActive = (persisted as { lastActiveConv?: string } | null)?.lastActiveConv;
        const initialId =
          (lastActive && persistedConvs.find((c) => c.id === lastActive)?.id) ??
          persistedConvs[0].id;
        setActiveConversationId(initialId);
        activeIdRef.current = initialId;
        setHasLoaded(true);
      } else if (attempts < 2) {
        // First attempt returned empty — retry once after 500ms.
        await new Promise((r) => setTimeout(r, 500));
        if (!cancelled) await load();
      } else {
        setHasLoaded(true);
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  // Persist settings to the backend whenever they change after the
  // initial load. Debounced so dragging a slider doesn't thrash disk.
  useEffect(() => {
    if (!hasLoaded) return;
    const t = setTimeout(() => {
      SaveSettings({
        ...settings,
        lastActiveConv: activeConversationId ?? '',
      }).catch((e) => console.error('SaveSettings:', e));
    }, 400);
    return () => clearTimeout(t);
  }, [hasLoaded, settings, activeConversationId]);

  // Persist conversations to the backend. Slightly longer debounce
  // because streaming produces many updates per second.
  useEffect(() => {
    if (!hasLoaded) return;
    const t = setTimeout(() => {
      SaveConversations(conversations as Parameters<typeof SaveConversations>[0]).catch((e) =>
        console.error('SaveConversations:', e),
      );
    }, 800);
    return () => clearTimeout(t);
  }, [hasLoaded, conversations]);

  const activeConversation = useMemo(
    () => conversations.find((c) => c.id === activeConversationId) ?? null,
    [conversations, activeConversationId],
  );

  const updateActive = useCallback((updater: (conv: Conversation) => Conversation) => {
    const id = activeIdRef.current;
    if (!id) return;
    setConversations((prev) =>
      prev.map((c) => (c.id === id ? updater(c) : c)),
    );
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

  // Functional setter avoids the stale-closure trap that the previous
  // implementation had (it captured `conversations` and `activeConversationId`
  // from the surrounding render, so deleting the *active* conversation could
  // pick a sibling that was filtered out before the next state read).
  const deleteConversation = useCallback((id: string) => {
    setConversations((prev) => {
      const remaining = prev.filter((c) => c.id !== id);
      setActiveConversationId((curr) => {
        if (curr !== id) return curr;
        const next = remaining[0] ?? null;
        activeIdRef.current = next ? next.id : null;
        return next ? next.id : null;
      });
      return remaining;
    });
  }, []);

  // ---- Stream event handlers -------------------------------------------
  // All handlers operate on the *active* conversation via updateActive,
  // which reads activeIdRef.current at call time. This way the EventsOn
  // subscriptions below can be registered exactly once for the app's
  // lifetime instead of being torn down / rebuilt on every state change.

  const handleStreamTextDelta = useCallback((delta: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => ({ ...m, content: m.content + delta })),
    );
  }, [updateActive]);

  const handleStreamThinkingDelta = useCallback((delta: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => ({
        ...m,
        thinking: (m.thinking || '') + delta,
      })),
    );
  }, [updateActive]);

  const handleStreamToolCallStart = useCallback((data: string) => {
    try {
      const toolCall = JSON.parse(data) as { id: string; name: string };
      updateActive((conv) =>
        updateLastAssistant(conv, (m) => ({
          ...m,
          toolCalls: [
            ...(m.toolCalls || []),
            { id: toolCall.id, name: toolCall.name, arguments: '' },
          ],
        })),
      );
    } catch (e) {
      console.error('Error parsing tool call start:', e);
    }
  }, [updateActive]);

  const handleStreamToolCallDelta = useCallback((delta: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => {
        const toolCalls = m.toolCalls;
        if (!toolCalls || toolCalls.length === 0) return m;
        const updated = [...toolCalls];
        updated[updated.length - 1] = {
          ...updated[updated.length - 1],
          arguments: updated[updated.length - 1].arguments + delta,
        };
        return { ...m, toolCalls: updated };
      }),
    );
  }, [updateActive]);

  const handleStreamToolCallEnd = useCallback((args: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => {
        const toolCalls = m.toolCalls;
        if (!toolCalls || toolCalls.length === 0) return m;
        const updated = [...toolCalls];
        updated[updated.length - 1] = {
          ...updated[updated.length - 1],
          arguments: args,
        };
        return { ...m, toolCalls: updated };
      }),
    );
  }, [updateActive]);

  const handleStreamToolExecStart = useCallback((data: string) => {
    try {
      const info = JSON.parse(data) as { id: string; name: string };
      updateActive((conv) =>
        updateLastAssistant(conv, (m) => ({
          ...m,
          toolExecutions: [
            ...(m.toolExecutions || []),
            { id: info.id, name: info.name } as ToolExecution,
          ],
        })),
      );
    } catch (e) {
      console.error('Error parsing tool exec start:', e);
    }
  }, [updateActive]);

  const handleStreamToolExecEnd = useCallback((result: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => {
        const execs = m.toolExecutions;
        if (!execs || execs.length === 0) return m;
        const updated = [...execs];
        updated[updated.length - 1] = { ...updated[updated.length - 1], result, isError: false };
        return { ...m, toolExecutions: updated };
      }),
    );
  }, [updateActive]);

  const handleStreamToolExecError = useCallback((result: string) => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) => {
        const execs = m.toolExecutions;
        if (!execs || execs.length === 0) return m;
        const updated = [...execs];
        const lastExec = updated[updated.length - 1];
        updated[updated.length - 1] = { ...lastExec, result, isError: true };
        // Also append the error to the content so it's visible even if
        // the tool block is collapsed or the stream-done event causes a
        // re-render that obscures the inline tool execution display.
        const errorText = `Tool "${lastExec.name}" failed: ${result}`;
        return { ...m, content: m.content ? m.content + '\n\n---\n\n' + errorText : errorText, toolExecutions: updated };
      }),
    );
  }, [updateActive]);

  const handleStreamDone = useCallback(() => {
    updateActive((conv) =>
      updateLastAssistant(conv, (m) =>
        m.timestamp ? m : { ...m, timestamp: formatTimestamp(new Date()) },
      ),
    );
    setIsLoading(false);
  }, [updateActive]);

  const handleStreamError = useCallback((error: string) => {
    console.error('Stream error:', error);
    updateActive((conv) =>
      updateLastAssistant(conv, (m) =>
        m.content ? m : { ...m, content: error, timestamp: formatTimestamp(new Date()) },
      ),
    );
    setIsLoading(false);
  }, [updateActive]);

  // Register all stream listeners exactly once. Each handler reads its
  // dependencies through closures / refs so they always see fresh state.
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []); // intentional: subscriptions live for the app lifetime

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

      const now = Date.now();
      const userMessage: Message = {
        id: `user-${now}`,
        role: 'user',
        content: message,
        timestamp: formatTimestamp(new Date()),
      };
      const assistantMessage: Message = {
        id: `assistant-${now}`,
        role: 'assistant',
        content: '',
        timestamp: '',
      };

      const title = message.length > 30 ? message.slice(0, 30) + '…' : message;

      setConversations((prev) =>
        prev.map((c) => {
          if (c.id !== targetId) return c;
          const isFirst = c.messages.length === 0;
          return {
            ...c,
            title: isFirst ? title : c.title,
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
            return updateLastAssistant(c, (m) =>
              m.content
                ? m
                : {
                    ...m,
                    content: 'Sorry, something went wrong. Please try again.',
                    timestamp: formatTimestamp(new Date()),
                  },
            );
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
    <div className={`app-shell${sidebarCollapsed ? ' sidebar-collapsed' : ''}`}>
      <Sidebar
        conversations={conversations}
        activeConversation={activeConversationId}
        workingDir={settings.workingDir}
        collapsed={sidebarCollapsed}
        onSelectConversation={selectConversation}
        onCreateNewConversation={createNewConversation}
        onDeleteConversation={deleteConversation}
        onOpenSettings={() => setIsSettingsOpen(true)}
      />

      <main className="main-frame">
        <header className="topbar">
          <div className="topbar-left">
            <button className="icon-btn sidebar-toggle" onClick={() => setSidebarCollapsed((p) => !p)} aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
              <MenuOutlined size={18} />
            </button>
            <div className="breadcrumb">
              <span className="crumb-root">Crux Agent</span>
              <span className="crumb-sep">/</span>
              <strong className="crumb-current">
                {activeConversation ? activeConversation.title : 'Workspace'}
              </strong>
            </div>
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