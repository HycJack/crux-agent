import { useState, useEffect } from 'react';
import {
  CloseOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  FolderIcon,
  RefreshOutlined,
  SaveOutlined,
  Brain,
  Settings2,
  CodeOutlined,
  WrenchOutlined,
} from '../icons';
import {
  GetModels,
  PickWorkingDir,
  SetWorkingDir,
  SetAutoLearnEnabled,
  GetSkills,
  GetSkillContent,
  GetMemories,
  SetMemory,
  DeleteMemory,
  ClearMemory,
  GetToolList,
  GetCompactionStatus,
  ReloadSkills,
} from '../../wailsjs/go/main/App';
import type { Settings } from '../types';

interface SettingsPanelProps {
  isOpen: boolean;
  onClose: () => void;
  currentSettings: Settings;
  onSave: (settings: Settings) => void;
}

interface ModelInfo {
  id: string;
  name: string;
  reasoning?: boolean;
  thinkingLevelMap?: Record<string, string>;
}

type TabId = 'general' | 'skills' | 'memory' | 'system';

const tabs: { id: TabId; label: string; icon: React.ReactNode }[] = [
  { id: 'general', label: 'General', icon: <Settings2 size={14} /> },
  { id: 'skills', label: 'Skills', icon: <Brain size={14} /> },
  { id: 'memory', label: 'Memory', icon: <CodeOutlined size={14} /> },
  { id: 'system', label: 'System', icon: <WrenchOutlined size={14} /> },
];

const providerLabel: Record<Settings['provider'], string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
};

const providerBaseUrl: Record<Settings['provider'], string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com/v1',
};

export default function SettingsPanel({ isOpen, onClose, currentSettings, onSave }: SettingsPanelProps) {
  const [activeTab, setActiveTab] = useState<TabId>('general');
  const [settings, setSettings] = useState<Settings>(currentSettings);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [showCustomModel, setShowCustomModel] = useState(false);
  const [showApiKey, setShowApiKey] = useState(false);
  const [saved, setSaved] = useState(false);
  const [workingDirError, setWorkingDirError] = useState<string>('');

  // Tab-specific state
  const [skills, setSkills] = useState<string[]>([]);
  const [selectedSkill, setSelectedSkill] = useState<string | null>(null);
  const [skillContent, setSkillContent] = useState<string>('');
  const [memories, setMemories] = useState<Record<string, string>>({});
  const [newMemKey, setNewMemKey] = useState('');
  const [newMemValue, setNewMemValue] = useState('');
  const [tools, setTools] = useState<string[]>([]);
  const [compactionStatus, setCompactionStatus] = useState('');

  useEffect(() => {
    setSettings(currentSettings);
    setSaved(false);
    setWorkingDirError('');
  }, [currentSettings, isOpen]);

  // Auto-save when the panel is closed
  useEffect(() => {
    if (isOpen) return;
    if (JSON.stringify(settings) === JSON.stringify(currentSettings)) return;
    const final: Settings = {
      ...settings,
      model: settings.model === 'custom' ? settings.customModel : settings.model,
    };
    onSave(final);
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) return;
    SetWorkingDir(currentSettings.workingDir || '').catch(() => undefined);
  }, [isOpen, currentSettings.workingDir]);

  // Load tab data when tab changes
  useEffect(() => {
    if (!isOpen) return;
    switch (activeTab) {
      case 'skills':
        GetSkills().then(setSkills).catch(() => {});
        break;
      case 'memory':
        GetMemories().then(setMemories).catch(() => {});
        break;
      case 'system':
        Promise.all([
          GetToolList().then(setTools).catch(() => {}),
          GetCompactionStatus().then(setCompactionStatus).catch(() => {}),
        ]);
        break;
    }
  }, [isOpen, activeTab]);

  const fetchModels = async () => {
    setIsLoading(true);
    try {
      const modelList = await GetModels({
        provider: settings.provider,
        baseUrl: settings.baseUrl,
        apiKey: settings.apiKey,
      });
      setModels(modelList || []);
      if (modelList && modelList.length > 0 && !settings.model) {
        setSettings((prev) => ({ ...prev, model: modelList[0].id }));
      }
    } catch (error) {
      console.error('Failed to fetch models:', error);
    }
    setIsLoading(false);
  };

  const handleProviderChange = (provider: Settings['provider']) => {
    setSettings((prev) => ({
      ...prev,
      provider,
      model: '',
      baseUrl: providerBaseUrl[provider],
    }));
    setShowCustomModel(false);
    setModels([]);
  };

  const handleModelChange = (model: string) => {
    setSettings((prev) => ({ ...prev, model }));
    setShowCustomModel(model === 'custom');
    if (model !== 'custom') {
      setSettings((prev) => ({ ...prev, customModel: '' }));
    }
  };

  const handlePickDir = async () => {
    try {
      const dir = await PickWorkingDir();
      if (dir) {
        setSettings((prev) => ({ ...prev, workingDir: dir }));
        setWorkingDirError('');
      }
    } catch (error) {
      console.error('PickWorkingDir failed:', error);
      setWorkingDirError(String(error));
    }
  };

  const handleSave = async () => {
    const finalSettings: Settings = {
      ...settings,
      model: settings.model === 'custom' ? settings.customModel : settings.model,
    };
    try {
      if (finalSettings.workingDir) {
        await SetWorkingDir(finalSettings.workingDir);
      }
      // Push the auto-learn toggle to the backend immediately so the
      // StreamMessage loop honors it on the very next turn.
      await SetAutoLearnEnabled(!!finalSettings.autoLearn);
      onSave(finalSettings);
      setSaved(true);
      setTimeout(() => setSaved(false), 1600);
    } catch (error) {
      setWorkingDirError(String(error));
    }
  };

  const loadSkillContent = async (name: string) => {
    setSelectedSkill(name);
    const content = await GetSkillContent(name);
    setSkillContent(content);
  };

  const reloadSkills = async () => {
    const dir = settings.workingDir;
    if (dir) {
      await ReloadSkills(dir);
      const list = await GetSkills();
      setSkills(list || []);
    }
  };

  const addMemory = async () => {
    if (!newMemKey.trim() || !newMemValue.trim()) return;
    await SetMemory(newMemKey.trim(), newMemValue.trim());
    setNewMemKey('');
    setNewMemValue('');
    const updated = await GetMemories();
    setMemories(updated || {});
  };

  const deleteMemory = async (key: string) => {
    await DeleteMemory(key);
    const updated = await GetMemories();
    setMemories(updated || {});
  };

  const clearAllMemory = async () => {
    await ClearMemory();
    setMemories({});
  };

  if (!isOpen) return null;

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-card settings-modal-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title">Settings</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            <CloseOutlined size={16} />
          </button>
        </div>

        {/* Tab bar */}
        <div className="settings-tabs">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              className={`settings-tab ${activeTab === tab.id ? 'active' : ''}`}
              onClick={() => setActiveTab(tab.id)}
            >
              {tab.icon}
              <span>{tab.label}</span>
            </button>
          ))}
        </div>

        <div className="modal-body">
          {/* ═══ General tab ═══ */}
          {activeTab === 'general' && (
            <>
              {/* Provider */}
              <section className="settings-section">
                <label className="settings-label">Provider</label>
                <div className="segmented">
                  {(Object.keys(providerLabel) as Settings['provider'][]).map((p) => (
                    <button
                      key={p}
                      className={`segmented-item ${settings.provider === p ? 'active' : ''}`}
                      onClick={() => handleProviderChange(p)}
                    >
                      {providerLabel[p]}
                    </button>
                  ))}
                </div>
              </section>

              {/* API Key */}
              <section className="settings-section">
                <label className="settings-label">API Key</label>
                <div className="input-with-adornment">
                  <input
                    type={showApiKey ? 'text' : 'password'}
                    value={settings.apiKey}
                    onChange={(e) => setSettings((prev) => ({ ...prev, apiKey: e.target.value }))}
                    placeholder={settings.provider === 'openai' ? 'sk-...' : 'sk-ant-...'}
                    className="text-input"
                  />
                  <button
                    type="button"
                    className="input-adornment"
                    onClick={() => setShowApiKey((v) => !v)}
                    aria-label={showApiKey ? 'Hide' : 'Show'}
                  >
                    {showApiKey ? <EyeInvisibleOutlined size={16} /> : <EyeOutlined size={16} />}
                  </button>
                </div>
                <p className="settings-hint">
                  {settings.provider === 'openai'
                    ? 'Uses OPENAI_API_KEY env var if blank.'
                    : 'Uses ANTHROPIC_API_KEY env var if blank.'}
                </p>
              </section>

              {/* Base URL */}
              <section className="settings-section">
                <label className="settings-label">Base URL</label>
                <input
                  type="text"
                  value={settings.baseUrl}
                  onChange={(e) => setSettings((prev) => ({ ...prev, baseUrl: e.target.value }))}
                  placeholder={providerBaseUrl[settings.provider]}
                  className="text-input"
                />
              </section>

              {/* Model */}
              <section className="settings-section">
                <label className="settings-label">Model</label>
                <div className="input-with-adornment" style={{ flex: 1 }}>
                  <select
                    value={settings.model}
                    onChange={(e) => handleModelChange(e.target.value)}
                    disabled={isLoading}
                    className="text-input select-input"
                  >
                    <option value="" disabled>
                      Select a model
                    </option>
                    {models.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.name} ({m.id})
                      </option>
                    ))}
                    <option value="custom">Custom model…</option>
                  </select>
                  <button
                    type="button"
                    className="input-adornment"
                    onClick={fetchModels}
                    disabled={isLoading}
                    aria-label="Refresh models"
                    title="Refresh models"
                  >
                    <RefreshOutlined size={16} style={{ animation: isLoading ? 'status-spin 900ms linear infinite' : undefined }} />
                  </button>
                </div>
                {showCustomModel && (
                  <input
                    type="text"
                    value={settings.customModel}
                    onChange={(e) => setSettings((prev) => ({ ...prev, customModel: e.target.value }))}
                    placeholder="Enter custom model ID"
                    className="text-input"
                    style={{ marginTop: 8 }}
                  />
                )}
              </section>

              {/* Working directory */}
              <section className="settings-section">
                <label className="settings-label">Working directory</label>
                <p className="settings-hint" style={{ marginTop: 0 }}>
                  The agent reads, writes, and runs commands inside this directory.
                </p>
                <div className="input-with-adornment">
                  <input
                    type="text"
                    value={settings.workingDir}
                    onChange={(e) => setSettings((prev) => ({ ...prev, workingDir: e.target.value }))}
                    placeholder="C:\Users\you\Projects\my-app"
                    className="text-input"
                  />
                  <button
                    type="button"
                    className="input-adornment"
                    onClick={handlePickDir}
                    aria-label="Browse"
                    title="Browse"
                  >
                    <FolderIcon size={16} />
                  </button>
                </div>
                {workingDirError && <div className="settings-error">{workingDirError}</div>}
              </section>

              {/* Auto-learn */}
              <section className="settings-section">
                <label className="settings-label">Auto-learn</label>
                <div className="switch-row">
                  <span>Automatically extract memories from conversations</span>
                  <button
                    className={`switch ${settings.autoLearn ? 'on' : ''}`}
                    onClick={() => setSettings((prev) => ({ ...prev, autoLearn: !prev.autoLearn }))}
                    aria-pressed={!!settings.autoLearn}
                  >
                    <span className="switch-knob" />
                  </button>
                </div>
                <p className="settings-hint" style={{ marginTop: 0 }}>
                  When enabled, the agent will learn facts about you (name, preferences, etc.) from natural conversation and store them as long-term memory.
                </p>
              </section>

              {/* TTS */}
              <section className="settings-section">
                <label className="settings-label">Text-to-speech</label>
                <div className="switch-row">
                  <span>Enable TTS for assistant replies</span>
                  <button
                    className={`switch ${settings.ttsEnabled ? 'on' : ''}`}
                    onClick={() => setSettings((prev) => ({ ...prev, ttsEnabled: !prev.ttsEnabled }))}
                    aria-pressed={settings.ttsEnabled}
                  >
                    <span className="switch-knob" />
                  </button>
                </div>
                {settings.ttsEnabled && (
                  <select
                    value={settings.ttsVoice}
                    onChange={(e) => setSettings((prev) => ({ ...prev, ttsVoice: e.target.value }))}
                    className="text-input select-input"
                    style={{ marginTop: 8 }}
                  >
                    <option value="zh-CN">中文（普通话）</option>
                    <option value="en-US">English (US)</option>
                    <option value="en-GB">English (UK)</option>
                    <option value="ja-JP">日本語</option>
                    <option value="ko-KR">한국어</option>
                  </select>
                )}
              </section>
            </>
          )}

          {/* ═══ Skills tab ═══ */}
          {activeTab === 'skills' && (
            <section className="settings-section">
              <div className="settings-section-header">
                <label className="settings-label">Installed skills</label>
                <button className="btn-sm" onClick={reloadSkills}>
                  <RefreshOutlined size={12} />
                  Reload
                </button>
              </div>
              <p className="settings-hint">
                Skills are loaded from <code>skills/&lt;name&gt;/SKILL.md</code> in the working directory.
              </p>

              {skills.length === 0 ? (
                <div className="settings-empty">No skills found. Create a <code>skills/&lt;name&gt;/SKILL.md</code> file.</div>
              ) : (
                <div className="skills-list">
                  {skills.map((name) => (
                    <div key={name} className="skills-list-item">
                      <button
                        className={`skills-item-btn ${selectedSkill === name ? 'active' : ''}`}
                        onClick={() => loadSkillContent(name)}
                      >
                        <span className="skills-item-name">{name}</span>
                      </button>
                    </div>
                  ))}
                </div>
              )}

              {selectedSkill && skillContent && (
                <div className="skill-preview">
                  <div className="skill-preview-header">
                    <strong>{selectedSkill}</strong>
                  </div>
                  <pre className="skill-preview-body">{skillContent}</pre>
                </div>
              )}
            </section>
          )}

          {/* ═══ Memory tab ═══ */}
          {activeTab === 'memory' && (
            <section className="settings-section">
              <div className="settings-section-header">
                <label className="settings-label">Long-term memories ({Object.keys(memories).length})</label>
                {Object.keys(memories).length > 0 && (
                  <button className="btn-sm btn-danger-text" onClick={clearAllMemory}>
                    Clear all
                  </button>
                )}
              </div>

              {/* Add new memory */}
              <div className="memory-add-row">
                <input
                  type="text"
                  value={newMemKey}
                  onChange={(e) => setNewMemKey(e.target.value)}
                  placeholder="Key (e.g. user.name)"
                  className="text-input memory-key-input"
                />
                <input
                  type="text"
                  value={newMemValue}
                  onChange={(e) => setNewMemValue(e.target.value)}
                  placeholder="Value"
                  className="text-input memory-value-input"
                />
                <button className="btn-sm btn-primary-sm" onClick={addMemory} disabled={!newMemKey.trim() || !newMemValue.trim()}>
                  Add
                </button>
              </div>

              {Object.keys(memories).length === 0 ? (
                <div className="settings-empty">No memories stored yet.</div>
              ) : (
                <div className="memory-list">
                  {Object.entries(memories).map(([key, value]) => (
                    <div key={key} className="memory-item">
                      <div className="memory-item-content">
                        <span className="memory-key">{key}</span>
                        <span className="memory-value">{value}</span>
                      </div>
                      <button
                        className="memory-delete-btn"
                        onClick={() => deleteMemory(key)}
                        aria-label={`Delete ${key}`}
                        title="Delete"
                      >
                        <CloseOutlined size={12} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </section>
          )}

          {/* ═══ System tab ═══ */}
          {activeTab === 'system' && (
            <>
              {/* Available tools */}
              <section className="settings-section">
                <label className="settings-label">Registered tools ({tools.length})</label>
                <p className="settings-hint">
                  These tools are available to the agent. Tools include built-in operations, skill tools, and memory tools.
                </p>
                <div className="tools-list">
                  {tools.map((name) => (
                    <div key={name} className="tool-chip">{name}</div>
                  ))}
                  {tools.length === 0 && <div className="settings-empty">No tools registered. Send a message first.</div>}
                </div>
              </section>

              {/* Context compaction */}
              <section className="settings-section">
                <label className="settings-label">Context compaction</label>
                <p className="settings-hint">
                  Automatic context-window management prevents the conversation from growing too large for the LLM.
                </p>
                <div className="compaction-status">
                  <pre>{compactionStatus || 'No agent started yet.'}</pre>
                </div>
              </section>
            </>
          )}
        </div>

        <div className="modal-footer">
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button className={`btn-primary ${saved ? 'is-success' : ''}`} onClick={handleSave}>
            <SaveOutlined size={16} />
            <span>{saved ? 'Saved' : 'Save changes'}</span>
          </button>
        </div>
      </div>
    </div>
  );
}
