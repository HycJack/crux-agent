import { useState, useEffect } from 'react';
import { CloseOutlined, EyeInvisibleOutlined, EyeOutlined, FolderIcon, RefreshOutlined, SaveOutlined } from '../icons';
import { GetModels, PickWorkingDir, SetWorkingDir } from '../../wailsjs/go/main/App';
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
}

const providerLabel: Record<Settings['provider'], string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
};

const providerBaseUrl: Record<Settings['provider'], string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com/v1',
};

export default function SettingsPanel({ isOpen, onClose, currentSettings, onSave }: SettingsPanelProps) {
  const [settings, setSettings] = useState<Settings>(currentSettings);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [showCustomModel, setShowCustomModel] = useState(false);
  const [showApiKey, setShowApiKey] = useState(false);
  const [saved, setSaved] = useState(false);
  const [workingDirError, setWorkingDirError] = useState<string>('');

  useEffect(() => {
    setSettings(currentSettings);
    setSaved(false);
    setWorkingDirError('');
  }, [currentSettings, isOpen]);

  // Auto-save when the panel is closed, so that in-flight edits are
  // not silently discarded if the user clicks outside / presses Escape
  // without explicitly hitting the Save button.
  useEffect(() => {
    if (isOpen) return;
    // Only persist if the local draft differs from the current settings.
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
      onSave(finalSettings);
      setSaved(true);
      setTimeout(() => setSaved(false), 1600);
    } catch (error) {
      setWorkingDirError(String(error));
    }
  };

  if (!isOpen) return null;

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title">Settings</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            <CloseOutlined size={16} />
          </button>
        </div>

        <div className="modal-body">
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