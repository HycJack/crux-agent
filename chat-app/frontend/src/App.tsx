import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CancelStream,
  GetAutoLearnEnabled,
  GetModels,
  GetWorkingDir,
  LoadConversations,
  LoadSettings,
  ReadFileContent as ReadFileContentBackend,
  GetFileTreeExpanded as GetFileTreeExpandedBackend,
  ReadDir as ReadDirBackend,
  ResetAgent,
  SaveConversations,
  SaveSettings,
  SetAutoLearnEnabled,
  SetWorkingDir,
  StreamMessage,
} from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import ChatArea from './components/ChatArea';
import FilePreviewPanel from './components/FilePreviewPanel';
import FileTreePanel from './components/FileTreePanel';
import SettingsPanel from './components/SettingsPanel';
import Sidebar from './components/Sidebar';
import type { Conversation, Message, Settings, ToolExecution } from './types';
import { FolderIcon, MenuOutlined } from './icons';

interface ModelInfo {
  id: string;
  name: string;
  reasoning?: boolean;
  thinkingLevelMap?: Record<string, string>;
}

const defaultSettings: Settings = {
  provider: 'openai',
  apiKey: '',
  baseUrl: 'https://api.openai.com/v1',
  model: '',
  customModel: '',
  workingDir: '',
  ttsEnabled: false,
  ttsVoice: 'zh-CN',
  autoLearn: false,
  thinkingLevel: '',
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

/**
 * Trim a tool result to the first and last N characters, with a middle
 * truncation marker. This drastically reduces token consumption while
 * preserving the head (what was produced) and tail (errors / final lines).
 */
function trimToolResult(text: string, keep: number = 200): string {
  if (!text || text.length <= keep * 2 + 20) return text || '';
  return text.slice(0, keep) + `\n\n[... ${text.length - keep * 2} bytes omitted ...]\n\n` + text.slice(-keep);
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
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [showFileExplorer, setShowFileExplorer] = useState(false);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const sidebarRef = useRef<HTMLDivElement>(null);
  const [explorerWidth, setExplorerWidth] = useState('380px');

  const handleResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = sidebarRef.current?.offsetWidth ?? 380;

    const onMove = (ev: MouseEvent) => {
      const diff = startX - ev.clientX;
      const newW = Math.max(280, Math.min(800, startWidth + diff));
      setExplorerWidth(`${newW}px`);
    };

    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, []);

  const currentUtteranceRef = useRef<SpeechSynthesisUtterance | null>(null);
  const activeIdRef = useRef<string | null>(null);

  // streamingIdRef points at the conversation whose stream the backend is
  // currently driving. While it is set, all stream event handlers MUST
  // target this id — not activeIdRef — so that switching the visible
  // conversation in the middle of a stream does not leak deltas into the
  // newly selected conversation.
  const streamingIdRef = useRef<string | null>(null);

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
      // Sync the working directory to the Go backend on startup so that
      // file/shell tools resolve paths relative to the configured working
      // directory rather than the exe's current directory.
      const initialWorkingDir = backendDir || ((persisted && (persisted as any).workingDir) as string) || '';
      if (initialWorkingDir) {
        SetWorkingDir(initialWorkingDir).catch(() => {});
      }
      // Sync the backend's auto-learn flag with the persisted setting on
      // startup, then read the backend's authoritative state back so the
      // UI always reflects reality. The persisted file is the intent;
      // the backend in-memory flag is the runtime truth. If they diverge
      // (e.g. an external reload wiped the Go state), the backend wins.
      const persistedAutoLearn = (persisted && (persisted as any).autoLearn) as boolean | undefined;
      if (typeof persistedAutoLearn === 'boolean') {
        await SetAutoLearnEnabled(persistedAutoLearn).catch(() => {});
      }
      const backendAutoLearn = await GetAutoLearnEnabled().catch(() => null);
      if (typeof backendAutoLearn === 'boolean') {
        setSettings((prev) => ({ ...prev, autoLearn: backendAutoLearn }));
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

  // Fetch available models when settings are loaded
  useEffect(() => {
    if (!hasLoaded || !settings.apiKey) return;
    GetModels({
      provider: settings.provider,
      baseUrl: settings.baseUrl,
      apiKey: settings.apiKey,
    }).then((list) => {
      if (list) setModels(list);
    }).catch(() => {});
  }, [hasLoaded, settings.provider, settings.baseUrl, settings.apiKey]);

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
    // While a stream is in flight the deltas MUST go to the conversation
    // that initiated it, even if the user has since switched to another
    // conversation in the sidebar. Falls back to activeIdRef when no
    // stream is running so non-stream updates (title rename, etc.)
    // still target the visible conversation.
    const id = streamingIdRef.current ?? activeIdRef.current;
    if (!id) return;
    setConversations((prev) =>
      prev.map((c) => (c.id === id ? updater(c) : c)),
    );
  }, []);

  const createNewConversation = useCallback(() => {
    ResetAgent().catch(() => {});
    const conv = newConversation();
    setConversations((prev) => [conv, ...prev]);
    setActiveConversationId(conv.id);
    activeIdRef.current = conv.id;
  }, []);

  const selectConversation = useCallback((id: string) => {
    ResetAgent().catch(() => {});
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

  const renameConversation = useCallback((id: string, title: string) => {
    setConversations((prev) =>
      prev.map((c) => (c.id === id ? { ...c, title } : c)),
    );
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
        return { ...m, toolExecutions: updated };
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
    streamingIdRef.current = null;
  }, [updateActive]);

  const handleStreamError = useCallback((error: string) => {
    console.error('Stream error:', error);
    updateActive((conv) =>
      updateLastAssistant(conv, (m) =>
        m.content ? m : { ...m, content: error, timestamp: formatTimestamp(new Date()) },
      ),
    );
    setIsLoading(false);
    streamingIdRef.current = null;
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
    async (message: string, modelOverride?: string, thinkingLevel?: string) => {
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

      // Pin the stream target BEFORE issuing StreamMessage so any event
      // that fires while we're awaiting the IPC roundtrip still targets
      // the correct conversation.
      streamingIdRef.current = targetId;

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

      // Use the override model from chat input, or the setting's model
      const finalModel = modelOverride || (settings.model === 'custom' ? settings.customModel : settings.model);

      // Build conversation history for context restoration.
      // To save token space, tool calls and results are embedded into the
      // assistant message text instead of emitting separate "tool" role
      // messages. Each tool result is trimmed to the first/last N chars.
      const targetConv = conversations.find((c) => c.id === targetId);
      const historyMessages: Record<string, unknown>[] = targetConv?.messages.flatMap((m) => {
        if (m.role !== 'assistant') {
          return [{ role: m.role, content: m.content }];
        }
        // Build an assistant message that inlines tool execution summaries.
        let content = m.content;
        if (m.toolCalls && m.toolCalls.length > 0) {
          const summaries = (m.toolExecutions || []).map((te, i) => {
            const tc = m.toolCalls![i];
            const trimmed = trimToolResult(te.result || '');
            return `[Tool: ${te.name}]${te.isError ? ' (error)' : ''}\nArgs: ${tc.arguments}\nResult: ${trimmed}`;
          });
          content = content ? `${content}\n\n${summaries.join('\n\n')}` : summaries.join('\n\n');
        }
        return [{ role: 'assistant', content }];
      }) ?? [];

      try {
        await StreamMessage({
          message,
          provider: settings.provider,
          apiKey: settings.apiKey,
          baseUrl: settings.baseUrl,
          model: finalModel,
          thinkingLevel: thinkingLevel ?? settings.thinkingLevel ?? '',
          messages: JSON.stringify(historyMessages),
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
        streamingIdRef.current = null;
      }
    },
    [settings, stopSpeaking],
  );

  // Lightweight, self-dismissing toast. Renders into a fixed overlay so
  // any component can fire one without needing a portal or context.
  const [toast, setToast] = useState<string | null>(null);
  const showToast = useCallback((message: string) => {
    setToast(message);
    setTimeout(() => setToast((curr) => (curr === message ? null : curr)), 2200);
  }, []);

  const handleSaveSettings = useCallback((newSettings: Settings) => {
    setSettings(newSettings);
    // Propagate the auto-learn toggle to the backend immediately so
    // that subsequent StreamMessage calls apply the change.
    SetAutoLearnEnabled(!!newSettings.autoLearn).catch(() => {});
    showToast('Settings saved');
  }, [showToast]);

  const handleModelChange = useCallback((model: string) => {
    setSettings((prev) => ({ ...prev, model }));
  }, []);

  const handleThinkingLevelChange = useCallback((level: string) => {
    setSettings((prev) => ({ ...prev, thinkingLevel: level }));
  }, []);

  const handleSelectFile = useCallback((path: string) => {
    setSelectedFile(path);
    setShowFileExplorer(true);
  }, []);

  const handleClosePreview = useCallback(() => {
    setSelectedFile(null);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setIsSettingsOpen(false);
        if (selectedFile) {
          setSelectedFile(null);
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectedFile]);

  return (
    <div className={`app-shell${sidebarCollapsed ? ' sidebar-collapsed' : ''}`}>
      <Sidebar
        conversations={conversations}
        activeConversation={activeConversationId}
        workingDir={settings.workingDir}
        collapsed={sidebarCollapsed}
        explorerOpen={showFileExplorer}
        onSelectConversation={selectConversation}
        onCreateNewConversation={createNewConversation}
        onDeleteConversation={deleteConversation}
        onRenameConversation={renameConversation}
        onOpenSettings={() => setIsSettingsOpen(true)}
        onToggleExplorer={() => setShowFileExplorer((p) => !p)}
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
              <button className="btn-danger" onClick={() => {
                CancelStream().catch(() => undefined);
                setIsLoading(false);
                streamingIdRef.current = null;
              }}>
                Stop
              </button>
            )}
          </div>
        </header>

        <div className="main-body">
          <div className="main-chat">
            <ChatArea
              messages={activeConversation?.messages ?? []}
              isLoading={isLoading}
              onSendMessage={handleSendMessage}
              onStop={() => {
                CancelStream().catch(() => undefined);
                setIsLoading(false);
                streamingIdRef.current = null;
              }}
              onSpeak={speakText}
              onStopSpeak={stopSpeaking}
              speakingMessageId={speakingMessageId}
              workingDir={settings.workingDir}
              models={models}
              currentModel={settings.model === 'custom' ? settings.customModel : settings.model}
              currentThinkingLevel={settings.thinkingLevel ?? ''}
              onModelChange={handleModelChange}
              onThinkingLevelChange={handleThinkingLevelChange}
            />
          </div>

          <div
            className={`main-explorer-sidebar ${showFileExplorer ? 'expanded' : ''}`}
            ref={sidebarRef}
          >
            {/* Resizable handle – click or drag */}
            <div
              className="main-explorer-resize-handle"
              onMouseDown={handleResizeStart}
            />
            {/* Collapsed state: vertical icon strip */}
            {!showFileExplorer && (
              <button
                className="main-explorer-collapsed-toggle"
                onClick={() => setShowFileExplorer(true)}
                title="Open file explorer"
                aria-label="Open file explorer"
              >
                <FolderIcon size={18} />
              </button>
            )}
            {/* Expanded content */}
            <div className="main-explorer-content" style={{ width: explorerWidth }}>
              <div className="main-explorer-header">
                <button
                  className="main-explorer-collapse-btn"
                  onClick={() => setShowFileExplorer(false)}
                  title="Close file explorer"
                  aria-label="Close file explorer"
                >
                  <FolderIcon size={16} />
                </button>
                <div className="main-explorer-tabs">
                  <button
                    className={`main-explorer-tab ${!selectedFile ? 'active' : ''}`}
                    onClick={() => setSelectedFile(null)}
                  >
                    Files
                  </button>
                  {selectedFile && (
                    <button className="main-explorer-tab active">
                      Preview
                    </button>
                  )}
                </div>
              </div>
              <div className="main-explorer-body">
                {selectedFile ? (
                  <FilePreviewPanel
                    filePath={selectedFile}
                    readFileContent={ReadFileContentBackend}
                    onClose={handleClosePreview}
                  />
                ) : (
                  <FileTreePanel
                    workingDir={settings.workingDir}
                    onSelectFile={handleSelectFile}
                    selectedFile={selectedFile}
                    getFileTreeExpanded={GetFileTreeExpandedBackend}
                    readDir={ReadDirBackend}
                  />
                )}
              </div>
            </div>
          </div>
        </div>
      </main>

      <SettingsPanel
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        currentSettings={settings}
        onSave={handleSaveSettings}
      />

      {toast && (
        <div className="app-toast" role="status" aria-live="polite">
          {toast}
        </div>
      )}
    </div>
  );
}

export default App;