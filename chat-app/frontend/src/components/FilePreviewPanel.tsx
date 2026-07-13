import { useCallback, useEffect, useRef, useState } from 'react';
import OfficeHtmlViewer from './OfficeHtmlViewer';
import { RenderToHTML } from '../../wailsjs/go/main/App';

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
  renderToHtml?: (path: string) => Promise<string>;
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
  renderToHtml,
  onClose,
}: FilePreviewPanelProps) {
  const [fileData, setFileData] = useState<FileContent | null>(null);
  const [previewType, setPreviewType] = useState<PreviewType>('loading');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);
  const [officeHtml, setOfficeHtml] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  const loadFile = useCallback(async () => {
    if (!filePath) return;
    setLoading(true);
    setError(null);
    setFileData(null);
    setOfficeHtml(null);
    setZoom(1);

    const ptype = detectPreviewType(filePath);
    setPreviewType(ptype);

    // For office documents (docx, xlsx, pptx, pdf), try server-side HTML rendering first
    if (['pdf', 'docx', 'xlsx', 'pptx'].includes(ptype)) {
      const renderFn = renderToHtml || RenderToHTML;
      try {
        const html = await renderFn(filePath);
        if (html && html.length > 0) {
          setOfficeHtml(html);
          setLoading(false);
          return;
        }
      } catch {
        // Fall through to client-side rendering
      }
    }

    try {
      const data = await readFileContent(filePath);
      setFileData(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPreviewType('error');
    } finally {
      setLoading(false);
    }
  }, [filePath, readFileContent, renderToHtml]);

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

            {/* Office documents rendered server-side (DOCX, XLSX, PPTX) */}
            {officeHtml && ['docx', 'xlsx', 'pptx'].includes(previewType) && (
              <OfficeHtmlViewer html={officeHtml} fileName={fileName} />
            )}

            {/* PDF: prefer server-side render; skip OfficeHtmlViewer to avoid iframe nesting */}
            {previewType === 'pdf' && (
              officeHtml ? (
                // Backend returned base64 PDF data – embed directly
                <div className="preview-office-html">
                  <div className="preview-office-toolbar">
                    <div className="preview-office-toolbar-left">
                      <span className="preview-office-filename">{fileName}</span>
                    </div>
                  </div>
                  <div className="preview-office-iframe-wrapper" style={{ height: 'calc(100vh - 200px)' }}>
                    <iframe
                      src={`data:application/pdf;base64,${officeHtml}`}
                      sandbox="allow-same-origin"
                      style={{ width: '100%', height: '100%', border: 'none' }}
                      title={fileName}
                    />
                  </div>
                </div>
              ) : fileData?.isBinary && fileData?.encoding === 'base64' ? (
                <PdfPreview base64={fileData.content} />
              ) : null
            )}

            {/* DOCX preview - render with docx-preview (fallback if server-side failed) */}
            {!officeHtml && previewType === 'docx' && fileData?.isBinary && fileData?.encoding === 'base64' && (
              <DocxPreview base64={fileData.content} fileName={fileName} />
            )}

            {/* XLSX preview (fallback if server-side failed) */}
            {!officeHtml && previewType === 'xlsx' && fileData?.isBinary && fileData?.encoding === 'base64' && (
              <XlsxPreview base64={fileData.content} fileName={fileName} />
            )}

            {/* PPTX preview (fallback if server-side failed) */}
            {!officeHtml && previewType === 'pptx' && fileData?.isBinary && fileData?.encoding === 'base64' && (
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

// ─── PDF preview (pdfjs-dist) ────────────────────────────────────
function PdfPreview({ base64 }: { base64: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pageCount, setPageCount] = useState(0);

  useEffect(() => {
    let cancelled = false;
    async function renderPdf() {
      try {
        const pdfjsLib = await import('pdfjs-dist');
        pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
          'pdfjs-dist/build/pdf.worker.min.mjs',
          import.meta.url,
        ).toString();

        // Decode base64 to bytes – more robust than manual atob+loop
        const binaryStr = atob(base64);
        const bytes = new Uint8Array(binaryStr.length);
        for (let i = 0; i < binaryStr.length; i++) {
          bytes[i] = binaryStr.charCodeAt(i);
        }

        const pdf = await pdfjsLib.getDocument({ data: bytes }).promise;
        if (cancelled) return;

        setPageCount(pdf.numPages);
        const container = containerRef.current;
        if (!container) return;

        for (let i = 1; i <= pdf.numPages; i++) {
          if (cancelled) return;
          const page = await pdf.getPage(i);
          const viewport = page.getViewport({ scale: 1.4 });

          const canvas = document.createElement('canvas');
          canvas.className = 'pdf-page-canvas';
          canvas.height = viewport.height;
          canvas.width = viewport.width;
          container.appendChild(canvas);

          const ctx = canvas.getContext('2d');
          if (!ctx) {
            if (!cancelled) setError('Canvas 2D context not available');
            break;
          }
          await page.render({
            canvas,
            viewport,
          }).promise;
        }
        if (!cancelled) setLoading(false);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
          setLoading(false);
        }
      }
    }
    renderPdf();
    return () => {
      cancelled = true;
      // Clean up canvases
      const container = containerRef.current;
      if (container) container.innerHTML = '';
    };
  }, [base64]);

  if (loading) {
    return (
      <div className="file-preview-status">
        <div className="file-preview-spinner" />
        <span>Rendering PDF ({pageCount > 0 ? `${pageCount} pages` : '...'})</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="file-preview-error">
        <div className="file-preview-error-icon">!</div>
        <div className="file-preview-error-text">PDF render error: {error}</div>
      </div>
    );
  }

  return (
    <div className="preview-scrollable preview-pdf-container" ref={containerRef} />
  );
}

// ─── DOCX preview (docx-preview) ────────────────────────────────
function DocxPreview({ base64, fileName }: { base64: string; fileName: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function renderDocx() {
      try {
        const { renderAsync } = await import('docx-preview');

        const binaryStr = atob(base64);
        const bytes = new Uint8Array(binaryStr.length);
        for (let i = 0; i < binaryStr.length; i++) {
          bytes[i] = binaryStr.charCodeAt(i);
        }
        const blob = new Blob([bytes], {
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        });

        const container = containerRef.current;
        if (!container) return;

        await renderAsync(blob, container, undefined, {
          className: 'docx-preview-body',
          inWrapper: true,
          ignoreWidth: false,
          ignoreHeight: true,
          breakPages: true,
        });

        if (!cancelled) setLoading(false);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
          setLoading(false);
        }
      }
    }

    renderDocx();
    return () => {
      cancelled = true;
      // Clean up rendered content
      const container = containerRef.current;
      if (container) container.innerHTML = '';
    };
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

  return <div className="preview-scrollable preview-docx-container" ref={containerRef} />;
}

// ─── XLSX preview (xlsx) ─────────────────────────────────────────
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

// ─── PPTX preview (placeholder) ──────────────────────────────────
function PptxPreview({ base64, fileName }: { base64: string; fileName: string }) {
  return (
    <div className="preview-placeholder">
      <div className="preview-placeholder-icon">P</div>
      <p className="preview-placeholder-title">{fileName}</p>
      <p className="preview-placeholder-text">
        PPTX files can not be previewed inline. Please open this file with an external application (e.g. PowerPoint, LibreOffice).
      </p>
      <p className="preview-placeholder-size">
        {(base64.length * 3) / 4 / 1024 > 1024
          ? `${((base64.length * 3) / 4 / (1024 * 1024)).toFixed(1)} MB`
          : `${((base64.length * 3) / 4 / 1024).toFixed(1)} KB`}
      </p>
    </div>
  );
}
