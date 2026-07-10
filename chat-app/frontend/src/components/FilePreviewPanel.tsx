import { useCallback, useEffect, useRef, useState } from 'react';

interface FileContent {
  name: string;
  path: string;
  content: string;
  size: number;
  isBinary: boolean;
  encoding?: string;
}

interface FilePreviewPanelProps {
  filePath: string | null;
  readFileContent: (path: string) => Promise<FileContent>;
  onClose: () => void;
}

type PreviewType = 'code' | 'markdown' | 'html' | 'pdf' | 'docx' | 'xlsx' | 'pptx' | 'image' | 'binary' | 'loading' | 'error';

function detectPreviewType(name: string): PreviewType {
  const ext = name.split('.').pop()?.toLowerCase() || '';

  if (['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp', 'ico'].includes(ext)) return 'image';
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  if (ext === 'html' || ext === 'htm') return 'html';
  if (ext === 'pdf') return 'pdf';
  if (ext === 'docx') return 'docx';
  if (ext === 'xlsx') return 'xlsx';
  if (ext === 'pptx') return 'pptx';

  return 'code';
}

// Simple markdown renderer for preview
function SimpleMarkdown({ content }: { content: string }) {
  const html = content
    // Headers
    .replace(/^### (.+)$/gm, '<h3>$1</h3>')
    .replace(/^## (.+)$/gm, '<h2>$1</h2>')
    .replace(/^# (.+)$/gm, '<h1>$1</h1>')
    // Bold & italic
    .replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>')
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    // Inline code
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    // Links
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank">$1</a>')
    // Images
    .replace(/!\[([^\]]*)\]\(([^)]+)\)/g, '<img alt="$1" src="$2" />')
    // Horizontal rules
    .replace(/^---$/gm, '<hr />')
    // Blockquotes
    .replace(/^> (.+)$/gm, '<blockquote>$1</blockquote>')
    // Unordered lists
    .replace(/^[\s]*[-*+] (.+)$/gm, '<li>$1</li>')
    // Ordered lists
    .replace(/^[\s]*\d+\. (.+)$/gm, '<li>$1</li>')
    // Code blocks
    .replace(/```(\w*)\n([\s\S]*?)```/g, '<pre><code class="language-$1">$2</code></pre>')
    // Line breaks
    .replace(/\n\n/g, '</p><p>')
    .replace(/\n/g, '<br />');

  return (
    <div
      className="preview-markdown-body"
      dangerouslySetInnerHTML={{ __html: `<p>${html}</p>` }}
    />
  );
}

// HTML sanitizer for preview (very simple - just strips scripts)
function sanitizeHtml(html: string): string {
  return html.replace(/<script[\s\S]*?<\/script>/gi, '<!-- scripts removed -->');
}

// File type badge
function fileTypeBadge(name: string): { label: string; color: string } {
  const ext = name.split('.').pop()?.toLowerCase() || '';
  const map: Record<string, { label: string; color: string }> = {
    pdf: { label: 'PDF', color: '#ec1c24' },
    docx: { label: 'DOCX', color: '#2b579a' },
    xlsx: { label: 'XLSX', color: '#217346' },
    pptx: { label: 'PPTX', color: '#d24726' },
    md: { label: 'MD', color: '#083fa1' },
    html: { label: 'HTML', color: '#e34f26' },
    htm: { label: 'HTML', color: '#e34f26' },
  };
  return map[ext] || { label: ext.toUpperCase(), color: '#666' };
}

export default function FilePreviewPanel({
  filePath,
  readFileContent,
  onClose,
}: FilePreviewPanelProps) {
  const [fileData, setFileData] = useState<FileContent | null>(null);
  const [previewType, setPreviewType] = useState<PreviewType>('loading');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  const loadFile = useCallback(async () => {
    if (!filePath) return;
    setLoading(true);
    setError(null);
    setFileData(null);
    setZoom(1);

    const ptype = detectPreviewType(filePath);
    setPreviewType(ptype);

    try {
      const data = await readFileContent(filePath);
      setFileData(data);

      // Binary files that are images need special handling
      if (ptype === 'image' && data.isBinary && data.encoding === 'base64') {
        // Image will be shown via data URL
      } else if (data.isBinary && ptype !== 'image') {
        // For docx/xlsx/pptx, we have the raw binary data base64 encoded
        // We still show what we can
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPreviewType('error');
    } finally {
      setLoading(false);
    }
  }, [filePath, readFileContent]);

  useEffect(() => {
    loadFile();
  }, [loadFile]);

  // Handle zoom for iframe-based previews
  useEffect(() => {
    const doc = iframeRef.current?.contentDocument;
    if (!doc?.body) return;
    (doc.body.style as any).zoom = `${zoom}`;
  }, [zoom, fileData]);

  if (!filePath) return null;

  const fileName = filePath.split(/[\\/]/).pop() || 'Unknown';

  const badge = fileTypeBadge(fileName);

  const zoomIn = () => setZoom((z) => Math.min(z + 0.15, 3));
  const zoomOut = () => setZoom((z) => Math.max(z - 0.15, 0.25));
  const zoomReset = () => setZoom(1);

  return (
    <div className="file-preview-panel">
      {/* Toolbar */}
      <div className="file-preview-toolbar">
        <div className="file-preview-toolbar-left">
          <span className="file-preview-badge" style={{ background: badge.color }}>
            {badge.label}
          </span>
          <span className="file-preview-filename" title={filePath}>
            {fileName}
          </span>
          {fileData && (
            <span className="file-preview-meta">
              {fileData.size > 1024
                ? `${(fileData.size / 1024).toFixed(1)} KB`
                : `${fileData.size} B`}
            </span>
          )}
        </div>
        <div className="file-preview-toolbar-right">
          {/* Zoom controls for non-code previews */}
          {previewType !== 'code' && previewType !== 'loading' && previewType !== 'error' && (
            <div className="file-preview-zoom-group">
              <button className="file-preview-zoom-btn" onClick={zoomOut} title="Zoom out">
                -
              </button>
              <span className="file-preview-zoom-level">{Math.round(zoom * 100)}%</span>
              <button className="file-preview-zoom-btn" onClick={zoomIn} title="Zoom in">
                +
              </button>
              <button className="file-preview-zoom-btn" onClick={zoomReset} title="Reset zoom">
                R
              </button>
            </div>
          )}
          <button className="file-preview-close-btn" onClick={onClose} title="Close preview">
            &times;
          </button>
        </div>
      </div>

      {/* Content area */}
      <div className="file-preview-content">
        {loading && (
          <div className="file-preview-status">
            <div className="file-preview-spinner" />
            <span>Loading {fileName}...</span>
          </div>
        )}

        {error && (
          <div className="file-preview-error">
            <div className="file-preview-error-icon">!</div>
            <div className="file-preview-error-text">{error}</div>
            <button className="file-preview-retry-btn" onClick={loadFile}>
              Retry
            </button>
          </div>
        )}

        {!loading && !error && fileData && (
          <>
            {/* Code preview */}
            {previewType === 'code' && !fileData.isBinary && (
              <div className="preview-code-wrapper">
                <div className="preview-code-line-numbers">
                  {(fileData.content.match(/\n/g)?.length || 0) + 1 > 0 && (
                    Array.from(
                      { length: (fileData.content.match(/\n/g)?.length || 0) + 1 },
                      (_, i) => (
                        <div key={i + 1} className="preview-code-line-num">
                          {i + 1}
                        </div>
                      ),
                    )
                  )}
                </div>
                <pre className="preview-code">
                  <code>{fileData.content}</code>
                </pre>
              </div>
            )}

            {/* Markdown preview */}
            {previewType === 'markdown' && !fileData.isBinary && (
              <div className="preview-scrollable">
                <SimpleMarkdown content={fileData.content} />
              </div>
            )}

            {/* HTML preview */}
            {previewType === 'html' && !fileData.isBinary && (
              <div className="preview-html-container-inner">
                <iframe
                  ref={iframeRef}
                  srcDoc={sanitizeHtml(fileData.content)}
                  sandbox="allow-same-origin"
                  className="preview-iframe"
                  title={fileName}
                />
              </div>
            )}

            {/* Image preview */}
            {previewType === 'image' && (
              <div className="preview-image-container">
                {fileData.isBinary && fileData.encoding === 'base64' ? (
                  <img
                    src={`data:image/${fileName.split('.').pop()};base64,${fileData.content}`}
                    alt={fileName}
                    className="preview-image"
                    style={{ transform: `scale(${zoom})` }}
                  />
                ) : (
                  <img
                    src={`data:image/${fileName.split('.').pop()};base64,${btoa(fileData.content)}`}
                    alt={fileName}
                    className="preview-image"
                    style={{ transform: `scale(${zoom})` }}
                  />
                )}
              </div>
            )}

            {/* PDF preview - embed using iframe with browser's native PDF viewer */}
            {previewType === 'pdf' && fileData.isBinary && fileData.encoding === 'base64' && (
              <div className="preview-pdf-container-inner">
                <iframe
                  ref={iframeRef}
                  src={`data:application/pdf;base64,${fileData.content}`}
                  className="preview-iframe"
                  title={fileName}
                />
              </div>
            )}

            {/* DOCX preview - render in iframe */}
            {previewType === 'docx' && fileData.isBinary && fileData.encoding === 'base64' && (
              <DocxPreview base64={fileData.content} fileName={fileName} />
            )}

            {/* XLSX preview */}
            {previewType === 'xlsx' && fileData.isBinary && fileData.encoding === 'base64' && (
              <XlsxPreview base64={fileData.content} fileName={fileName} />
            )}

            {/* PPTX preview */}
            {previewType === 'pptx' && fileData.isBinary && fileData.encoding === 'base64' && (
              <PptxPreview base64={fileData.content} fileName={fileName} />
            )}

            {/* Unknown binary */}
            {fileData.isBinary && !['pdf', 'docx', 'xlsx', 'pptx', 'image'].includes(previewType) && (
              <div className="preview-binary-placeholder">
                <div className="preview-binary-icon">B</div>
                <p>Binary file: {fileName}</p>
                <p className="preview-binary-size">
                  {fileData.size > 1024 * 1024
                    ? `${(fileData.size / (1024 * 1024)).toFixed(1)} MB`
                    : `${(fileData.size / 1024).toFixed(1)} KB`}
                </p>
                <p className="preview-binary-note">
                  Preview not available for this file type. Open it with an external application.
                </p>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// Stub component for DOCX preview (requires docx-preview library)
function DocxPreview({ base64, fileName }: { base64: string; fileName: string }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function renderDocx() {
      try {
        // Dynamic import of docx-preview
        const { renderAsync } = await import('docx-preview');

        const binaryStr = atob(base64);
        const bytes = new Uint8Array(binaryStr.length);
        for (let i = 0; i < binaryStr.length; i++) {
          bytes[i] = binaryStr.charCodeAt(i);
        }
        const blob = new Blob([bytes], {
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        });

        const iframe = iframeRef.current;
        if (!iframe) return;

        const iframeDoc = iframe.contentDocument || iframe.contentWindow?.document;
        if (!iframeDoc) {
          setError('Could not access preview container');
          setLoading(false);
          return;
        }

        iframeDoc.open();
        iframeDoc.write(
          `<!DOCTYPE html><html><head><style>body{margin:0;padding:16px;background:#e8e9eb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;}</style></head><body></body></html>`,
        );
        iframeDoc.close();

        await renderAsync(blob, iframeDoc.body, iframeDoc.head, {
          className: 'docx-preview-body',
          inWrapper: true,
          ignoreWidth: false,
          ignoreHeight: true,
          breakPages: true,
        });

        setLoading(false);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      }
    }

    renderDocx();
  }, [base64]);

  if (loading) {
    return (
      <div className="file-preview-status">
        <div className="file-preview-spinner" />
        <span>Rendering DOCX...</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="file-preview-error">
        <div className="file-preview-error-icon">!</div>
        <div className="file-preview-error-text">DOCX render error: {error}</div>
      </div>
    );
  }

  return (
    <iframe
      ref={iframeRef}
      sandbox="allow-same-origin"
      className="preview-iframe"
      title={fileName}
    />
  );
}

// Stub component for XLSX preview (requires xlsx library)
function XlsxPreview({ base64, fileName }: { base64: string; fileName: string }) {
  const [sheets, setSheets] = useState<{ name: string; html: string }[]>([]);
  const [activeSheet, setActiveSheet] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function renderXlsx() {
      try {
        const XLSX = await import('xlsx');

        const binaryStr = atob(base64);
        const bytes = new Uint8Array(binaryStr.length);
        for (let i = 0; i < binaryStr.length; i++) {
          bytes[i] = binaryStr.charCodeAt(i);
        }

        const wb = XLSX.read(bytes, { type: 'array' });
        const sheetData = wb.SheetNames.map((name) => {
          const sheet = wb.Sheets[name];
          const html = XLSX.utils.sheet_to_html(sheet, { id: `sheet-${name}` });
          return { name, html };
        });
        setSheets(sheetData);
        setLoading(false);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      }
    }

    renderXlsx();
  }, [base64]);

  if (loading) {
    return (
      <div className="file-preview-status">
        <div className="file-preview-spinner" />
        <span>Rendering XLSX...</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="file-preview-error">
        <div className="file-preview-error-icon">!</div>
        <div className="file-preview-error-text">XLSX render error: {error}</div>
      </div>
    );
  }

  return (
    <div className="preview-xlsx-wrapper">
      {sheets.length > 1 && (
        <div className="preview-xlsx-tabs">
          {sheets.map((s, i) => (
            <button
              key={s.name}
              className={`preview-xlsx-tab ${i === activeSheet ? 'active' : ''}`}
              onClick={() => setActiveSheet(i)}
            >
              {s.name}
            </button>
          ))}
        </div>
      )}
      <div
        className="preview-xlsx-content"
        dangerouslySetInnerHTML={{ __html: sheets[activeSheet]?.html || '' }}
      />
    </div>
  );
}

// Stub component for PPTX preview
function PptxPreview({ base64, fileName }: { base64: string; fileName: string }) {
  return (
    <div className="preview-placeholder">
      <div className="preview-placeholder-icon">P</div>
      <p className="preview-placeholder-title">{fileName}</p>
      <p className="preview-placeholder-text">
        PPTX preview. Install pptxjs for full support.
      </p>
      <div className="preview-placeholder-binary">
        <p>
          <strong>Fallback:</strong> This is a <code>.pptx</code> file (
          {(base64.length * 3) / 4 / 1024 > 1024
            ? `${((base64.length * 3) / 4 / (1024 * 1024)).toFixed(1)} MB`
            : `${((base64.length * 3) / 4 / 1024).toFixed(1)} KB`}
          ). You can open it externally to view its contents.
        </p>
      </div>
    </div>
  );
}
