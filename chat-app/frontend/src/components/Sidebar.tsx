import { useState } from 'react';
import {
  CheckOutlined2,
  DeleteOutlined,
  EditOutlined,
  FolderOpenOutlined,
  MenuOutlined,
  MessageOutlined,
  PlusOutlined,
  SettingOutlined,
  UserOutlined,
} from '../icons';
import type { Conversation } from '../types';

interface SidebarProps {
  conversations: Conversation[];
  activeConversation: string | null;
  workingDir: string;
  collapsed: boolean;
  onSelectConversation: (id: string) => void;
  onCreateNewConversation: () => void;
  onDeleteConversation: (id: string) => void;
  onRenameConversation: (id: string, title: string) => void;
  onOpenSettings: () => void;
}

export default function Sidebar({
  conversations,
  activeConversation,
  workingDir,
  collapsed,
  onSelectConversation,
  onCreateNewConversation,
  onDeleteConversation,
  onRenameConversation,
  onOpenSettings,
}: SidebarProps) {
  const dirName = workingDir ? workingDir.split(/[\\/]/).filter(Boolean).slice(-1)[0] : 'No workspace';

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingTitle, setEditingTitle] = useState('');
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const beginEdit = (conv: Conversation) => {
    setEditingId(conv.id);
    setEditingTitle(conv.title);
    setConfirmDeleteId(null);
  };

  const commitEdit = () => {
    if (!editingId) return;
    const trimmed = editingTitle.trim();
    if (trimmed) {
      onRenameConversation(editingId, trimmed);
    }
    setEditingId(null);
    setEditingTitle('');
  };

  const cancelEdit = () => {
    setEditingId(null);
    setEditingTitle('');
  };

  const requestDelete = (id: string) => {
    setConfirmDeleteId(id);
    setEditingId(null);
  };

  const confirmDelete = () => {
    if (confirmDeleteId) {
      onDeleteConversation(confirmDeleteId);
      setConfirmDeleteId(null);
    }
  };

  const cancelDelete = () => setConfirmDeleteId(null);

  if (collapsed) {
    return (
      <aside className="app-sidebar collapsed">
        <button className="icon-btn sidebar-toggle" onClick={() => {}} aria-label="Expand sidebar">
          <MenuOutlined size={18} />
        </button>
        <button className="nav-icon-btn" onClick={onCreateNewConversation} title="New chat">
          <EditOutlined size={18} />
        </button>
        <button className="nav-icon-btn" onClick={onOpenSettings} title="Settings">
          <SettingOutlined size={18} />
        </button>
        <div className="sidebar-spacer" />
        <button className="nav-icon-btn profile-link" title="Crux Agent">
          <UserOutlined size={18} />
        </button>
      </aside>
    );
  }

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
        <PlusOutlined size={16} />
        <span>New chat</span>
      </button>

      <div className="workspace-card" title={workingDir}>
        <FolderOpenOutlined size={18} />
        <div className="workspace-meta">
          <div className="workspace-label">Working directory</div>
          <div className="workspace-path">{dirName || 'Not set'}</div>
        </div>
      </div>

      <div className="history-list">
        <div className="history-list-header">
          <span className="history-list-title">
            <MessageOutlined size={16} />
            <span>History</span>
          </span>
        </div>
        <div className="history-list-items">
          {conversations.length === 0 && (
            <div className="history-empty">No conversations yet</div>
          )}
          {conversations.map((conv) => {
            const isActive = activeConversation === conv.id;
            const isEditing = editingId === conv.id;
            const isConfirmingDelete = confirmDeleteId === conv.id;
            return (
              <div
                key={conv.id}
                className={`history-item ${isActive ? 'active' : ''}`}
                onClick={() => {
                  if (isEditing || isConfirmingDelete) return;
                  onSelectConversation(conv.id);
                }}
                onDoubleClick={(e) => {
                  e.stopPropagation();
                  beginEdit(conv);
                }}
              >
                <span className="history-bullet" />
                <div className="history-meta">
                  {isEditing ? (
                    <input
                      autoFocus
                      className="history-title-input"
                      value={editingTitle}
                      onChange={(e) => setEditingTitle(e.target.value)}
                      onClick={(e) => e.stopPropagation()}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault();
                          commitEdit();
                        } else if (e.key === 'Escape') {
                          cancelEdit();
                        }
                      }}
                      onBlur={commitEdit}
                      maxLength={120}
                    />
                  ) : (
                    <div className="history-title">{conv.title}</div>
                  )}
                  {isConfirmingDelete ? (
                    <div className="history-confirm-row">
                      <span className="history-confirm-text">Delete this chat?</span>
                      <button
                        className="history-confirm-btn danger"
                        onClick={(e) => {
                          e.stopPropagation();
                          confirmDelete();
                        }}
                      >
                        Yes
                      </button>
                      <button
                        className="history-confirm-btn"
                        onClick={(e) => {
                          e.stopPropagation();
                          cancelDelete();
                        }}
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <div className="history-timestamp">{conv.timestamp}</div>
                  )}
                </div>
                {isEditing ? (
                  <button
                    className="history-action"
                    onClick={(e) => {
                      e.stopPropagation();
                      commitEdit();
                    }}
                    aria-label="Save title"
                    title="Save"
                  >
                    <CheckOutlined2 size={14} />
                  </button>
                ) : (
                  <button
                    className="history-action"
                    onClick={(e) => {
                      e.stopPropagation();
                      requestDelete(conv.id);
                    }}
                    aria-label="Delete"
                    title="Delete"
                  >
                    <DeleteOutlined size={14} />
                  </button>
                )}
              </div>
            );
          })}
        </div>
      </div>

      <div className="sidebar-footer">
        <button className="nav-item" onClick={onOpenSettings}>
          <SettingOutlined size={16} />
          <span>Settings</span>
        </button>
        <button className="nav-item profile-link">
          <UserOutlined size={16} />
          <span>Crux Agent</span>
        </button>
      </div>
    </aside>
  );
}