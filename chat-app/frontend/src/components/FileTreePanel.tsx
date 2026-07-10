import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ChevronDownOutlined,
  ChevronUpOutlined,
  FolderIcon,
  FolderOpenOutlined,
  CodeOutlined,
} from '../icons';

interface FileNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children?: FileNode[];
}

interface FileTreePanelProps {
  workingDir: string;
  onSelectFile: (path: string) => void;
  selectedFile: string | null;
  getFileTreeExpanded: () => Promise<FileNode>;
  readDir: (dirPath: string) => Promise<FileNode[]>;
}

// Map file extensions to icon/color indicators
function fileIcon(name: string): { icon: string; color: string } {
  const ext = name.split('.').pop()?.toLowerCase() || '';
  const iconMap: Record<string, { icon: string; color: string }> = {
    ts: { icon: 'TS', color: '#3178c6' },
    tsx: { icon: 'TSX', color: '#3178c6' },
    js: { icon: 'JS', color: '#f7df1e' },
    jsx: { icon: 'JSX', color: '#f7df1e' },
    go: { icon: 'GO', color: '#00add8' },
    rs: { icon: 'RS', color: '#dea584' },
    py: { icon: 'PY', color: '#3776ab' },
    json: { icon: '{}', color: '#5a5a5a' },
    md: { icon: 'MD', color: '#083fa1' },
    html: { icon: 'H', color: '#e34f26' },
    css: { icon: '#', color: '#1572b6' },
    scss: { icon: '$', color: '#cc6699' },
    yaml: { icon: 'Y', color: '#6c5ce7' },
    yml: { icon: 'Y', color: '#6c5ce7' },
    toml: { icon: 'T', color: '#6c5ce7' },
    dockerfile: { icon: 'D', color: '#2496ed' },
    sh: { icon: '>', color: '#4eaa25' },
    bat: { icon: 'B', color: '#4eaa25' },
    ps1: { icon: 'PS', color: '#012456' },
    sql: { icon: 'S', color: '#e38c00' },
    vue: { icon: 'V', color: '#4fc08d' },
    svelte: { icon: 'S', color: '#ff3e00' },
    java: { icon: 'J', color: '#007396' },
    c: { icon: 'C', color: '#555555' },
    cpp: { icon: 'C+', color: '#00599c' },
    h: { icon: 'H', color: '#a8b9cc' },
    rb: { icon: 'RB', color: '#cc342d' },
    php: { icon: 'P', color: '#777bb4' },
    swift: { icon: 'S', color: '#f05138' },
    kt: { icon: 'KT', color: '#7f52ff' },
    dart: { icon: 'D', color: '#0175c2' },
    lua: { icon: 'L', color: '#000080' },
    pdf: { icon: 'PDF', color: '#ec1c24' },
    docx: { icon: 'W', color: '#2b579a' },
    xlsx: { icon: 'X', color: '#217346' },
    pptx: { icon: 'P', color: '#d24726' },
    txt: { icon: 'T', color: '#5a5a5a' },
    gitignore: { icon: '.', color: '#e03c31' },
    env: { icon: 'E', color: '#e6a23c' },
  };
  return iconMap[ext] || { icon: 'F', color: '#8a8a8a' };
}

function formatSize(bytes?: number): string {
  if (bytes === undefined || bytes === 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

function TreeNode({
  node,
  depth,
  selectedFile,
  onSelectFile,
  onToggle,
  expandedDirs,
}: {
  node: FileNode;
  depth: number;
  selectedFile: string | null;
  onSelectFile: (path: string) => void;
  onToggle: (path: string) => void;
  expandedDirs: Set<string>;
}) {
  const isExpanded = expandedDirs.has(node.path);
  const isSelected = selectedFile === node.path;
  const icon = !node.isDir ? fileIcon(node.name) : null;

  const handleClick = () => {
    if (node.isDir) {
      onToggle(node.path);
    } else {
      onSelectFile(node.path);
    }
  };

  return (
    <>
      <div
        className={`file-tree-node ${isSelected ? 'selected' : ''}`}
        style={{ paddingLeft: `${12 + depth * 16}px` }}
        onClick={handleClick}
        title={node.path}
      >
        {node.isDir ? (
          <span className="file-tree-chevron">
            {isExpanded ? <ChevronDownOutlined size={12} /> : <ChevronUpOutlined size={12} />}
          </span>
        ) : (
          <span className="file-tree-spacer" />
        )}
        <span className="file-tree-icon">
          {node.isDir ? (
            isExpanded ? (
              <FolderOpenOutlined size={16} />
            ) : (
              <FolderIcon size={16} />
            )
          ) : (
            <span
              className="file-tree-file-badge"
              style={{ background: icon?.color || '#888' }}
            >
              {icon?.icon || 'F'}
            </span>
          )}
        </span>
        <span className="file-tree-name">{node.name}</span>
        {!node.isDir && node.size !== undefined && node.size > 0 && (
          <span className="file-tree-size">{formatSize(node.size)}</span>
        )}
      </div>
      {node.isDir && isExpanded && node.children && (
        <div className="file-tree-children">
          {node.children.length === 0 ? (
            <div className="file-tree-empty" style={{ paddingLeft: `${12 + (depth + 1) * 16}px` }}>
              (empty)
            </div>
          ) : (
            node.children.map((child) => (
              <TreeNode
                key={child.path}
                node={child}
                depth={depth + 1}
                selectedFile={selectedFile}
                onSelectFile={onSelectFile}
                onToggle={onToggle}
                expandedDirs={expandedDirs}
              />
            ))
          )}
        </div>
      )}
    </>
  );
}

export default function FileTreePanel({
  workingDir,
  onSelectFile,
  selectedFile,
  getFileTreeExpanded,
  readDir,
}: FileTreePanelProps) {
  const [tree, setTree] = useState<FileNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());
  const [searchQuery, setSearchQuery] = useState('');
  const [allFiles, setAllFiles] = useState<FileNode[]>([]);
  const [searchResults, setSearchResults] = useState<FileNode[]>([]);
  const [isSearching, setIsSearching] = useState(false);

  const loadTree = useCallback(async () => {
    if (!workingDir) return;
    setLoading(true);
    setError(null);
    try {
      const result = await getFileTreeExpanded();
      setTree(result);

      // Collect all files for search
      const files: FileNode[] = [];
      function collectFiles(node: FileNode) {
        if (!node.isDir) {
          files.push(node);
        }
        if (node.children) {
          node.children.forEach(collectFiles);
        }
      }
      collectFiles(result);
      setAllFiles(files);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [workingDir, getFileTreeExpanded]);

  useEffect(() => {
    loadTree();
  }, [loadTree]);

  // Expand root by default
  useEffect(() => {
    if (tree && expandedDirs.size === 0) {
      setExpandedDirs(new Set([tree.path]));

      // Also expand first level
      const dirs = new Set<string>([tree.path]);
      if (tree.children) {
        tree.children.forEach((child) => {
          if (child.isDir && child.name !== 'node_modules' && child.name !== '.git') {
            dirs.add(child.path);
          }
        });
      }
      setExpandedDirs(dirs);
    }
  }, [tree]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggleDir = useCallback((path: string) => {
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  }, []);

  const handleSearch = useCallback(
    (query: string) => {
      setSearchQuery(query);
      if (!query.trim()) {
        setSearchResults([]);
        setIsSearching(false);
        return;
      }
      setIsSearching(true);
      const q = query.toLowerCase();
      const results = allFiles.filter((f) => f.name.toLowerCase().includes(q));
      setSearchResults(results);
    },
    [allFiles],
  );

  const dirName = workingDir ? workingDir.split(/[\\/]/).filter(Boolean).slice(-1)[0] : 'No workspace';

  return (
    <div className="file-tree-panel">
      <div className="file-tree-header">
        <FolderOpenOutlined size={15} />
        <span className="file-tree-title">{dirName}</span>
        <button className="file-tree-refresh" onClick={loadTree} title="Refresh file tree">
          <CodeOutlined size={13} />
        </button>
      </div>

      <div className="file-tree-search">
        <input
          type="text"
          className="file-tree-search-input"
          placeholder="Search files..."
          value={searchQuery}
          onChange={(e) => handleSearch(e.target.value)}
        />
      </div>

      <div className="file-tree-scroll">
        {loading && <div className="file-tree-loading">Loading file tree...</div>}
        {error && <div className="file-tree-error">{error}</div>}
        {!loading && !error && tree && isSearching && (
          <div className="file-tree-search-results">
            {searchResults.length === 0 ? (
              <div className="file-tree-empty-search">No files matching "{searchQuery}"</div>
            ) : (
              searchResults.map((file) => (
                <div
                  key={file.path}
                  className={`file-tree-node file-tree-search-result ${selectedFile === file.path ? 'selected' : ''}`}
                  style={{ paddingLeft: '16px' }}
                  onClick={() => {
                    onSelectFile(file.path);
                    setIsSearching(false);
                    setSearchQuery('');
                  }}
                  title={file.path}
                >
                  <span className="file-tree-spacer" />
                  <span className="file-tree-icon">
                    <span
                      className="file-tree-file-badge"
                      style={{ background: fileIcon(file.name).color }}
                    >
                      {fileIcon(file.name).icon}
                    </span>
                  </span>
                  <span className="file-tree-name">{file.name}</span>
                  <span className="file-tree-size">{formatSize(file.size)}</span>
                </div>
              ))
            )}
          </div>
        )}
        {!loading && !error && tree && !isSearching && (
          <TreeNode
            node={tree}
            depth={0}
            selectedFile={selectedFile}
            onSelectFile={onSelectFile}
            onToggle={toggleDir}
            expandedDirs={expandedDirs}
          />
        )}
      </div>
    </div>
  );
}
