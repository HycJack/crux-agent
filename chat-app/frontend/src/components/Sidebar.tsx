import { EditOutlined, FolderOpenOutlined, MessageOutlined, PlusOutlined, SettingOutlined, UserOutlined } from '../icons';
import type { Conversation } from '../types';

interface SidebarProps {
  conversations: Conversation[];
  activeConversation: string | null;
  workingDir: string;
  onSelectConversation: (id: string) => void;
  onCreateNewConversation: () => void;
  onDeleteConversation: (id: string) => void;
  onOpenSettings: () => void;
}

export default function Sidebar({
  conversations,
  activeConversation,
  workingDir,
  onSelectConversation,
  onCreateNewConversation,
  onDeleteConversation,
  onOpenSettings,
}: SidebarProps) {
  const dirName = workingDir ? workingDir.split(/[\\/]/).filter(Boolean).slice(-1)[0] : 'No workspace';

  return (
    <aside className="app-sidebar">
      <div className="brand-block">
        <div className="brand-mark">
          <div className="brand-logo">C</div>
        </div>
        <div className="brand-text">
          <div className="brand">Crux Agent</div>
          <div className="bridge-pill">
            <span className="status-dot green" />
            <span>Local workspace</span>
          </div>
        </div>
      </div>

      <button className="primary-cta" onClick={onCreateNewConversation}>
        <EditOutlined />
        <span>New chat</span>
      </button>

      <div className="workspace-card" title={workingDir}>
        <FolderOpenOutlined />
        <div className="workspace-meta">
          <div className="workspace-label">Working directory</div>
          <div className="workspace-path">{dirName || 'Not set'}</div>
        </div>
      </div>

      <div className="history-list">
        <div className="history-list-header">
          <span className="history-list-title">
            <MessageOutlined />
            <span>History</span>
          </span>
        </div>
        <div className="history-list-items">
          {conversations.length === 0 && (
            <div className="history-empty">No conversations yet</div>
          )}
          {conversations.map((conv) => (
            <div
              key={conv.id}
              className={`history-item ${activeConversation === conv.id ? 'active' : ''}`}
              onClick={() => onSelectConversation(conv.id)}
            >
              <span className="history-bullet" />
              <div className="history-meta">
                <div className="history-title">{conv.title}</div>
                <div className="history-timestamp">{conv.timestamp}</div>
              </div>
              <button
                className="history-action"
                onClick={(e) => {
                  e.stopPropagation();
                  onDeleteConversation(conv.id);
                }}
                aria-label="Delete"
                title="Delete"
              >
                <PlusOutlined style={{ transform: 'rotate(45deg)' }} />
              </button>
            </div>
          ))}
        </div>
      </div>

      <div className="sidebar-footer">
        <button className="nav-item" onClick={onOpenSettings}>
          <SettingOutlined />
          <span>Settings</span>
        </button>
        <button className="nav-item profile-link">
          <UserOutlined />
          <span>Crux Agent</span>
        </button>
      </div>
    </aside>
  );
}