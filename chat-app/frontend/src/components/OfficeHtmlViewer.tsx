import { useCallback, useEffect, useRef, useState } from 'react';

interface OfficeHtmlViewerProps {
  html: string;
  fileName: string;
  onError?: (err: string) => void;
}

const ZOOM_STEP = 0.15;
const ZOOM_MIN = 0.25;
const ZOOM_MAX = 3;

export default function OfficeHtmlViewer({ html, fileName, onError }: OfficeHtmlViewerProps) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const fitZoomRef = useRef(1);

  const calcFitZoom = useCallback(() => {
    const container = containerRef.current;
    const iframeDoc = iframeRef.current?.contentDocument;
    if (!container || !iframeDoc?.body) return;
    const contentWidth = iframeDoc.body.scrollWidth;
    const containerWidth = container.clientWidth;
    if (contentWidth > 0 && containerWidth > 0 && contentWidth > containerWidth) {
      const fit = Math.min(containerWidth / contentWidth, 1);
      fitZoomRef.current = fit;
      setZoom(fit);
    }
  }, []);

  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) return;

    // Build the full HTML document with sandbox-friendly CSS
    const fullHtml = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
  body {
    margin: 0;
    padding: 16px 24px;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    font-size: 14px;
    line-height: 1.6;
    color: #1a1a1a;
    background: #fff;
  }
  /* DOCX styles */
  .docx-rendered { max-width: 800px; margin: 0 auto; }
  .docx-table { border-collapse: collapse; width: 100%; margin: 8px 0; }
  .docx-table td, .docx-table th { border: 1px solid #ccc; padding: 6px 10px; }
  /* XLSX styles */
  .xlsx-rendered { max-width: 100%; }
  .xlsx-sheet-name { font-size: 14px; font-weight: 600; margin: 8px 0 4px; color: #333; }
  .xlsx-sheet-sep { border: none; border-top: 1px solid #ddd; margin: 16px 0; }
  .xlsx-table { border-collapse: collapse; font-size: 12px; }
  .xlsx-table td { border: 1px solid #d0d0d0; padding: 4px 8px; min-width: 60px; white-space: nowrap; }
  .xlsx-table tr:nth-child(even) td { background: #f8f8f8; }
  /* PPTX styles */
  .pptx-rendered { max-width: 800px; margin: 0 auto; }
  .pptx-slide { background: #fff; border: 1px solid #ddd; border-radius: 6px; margin-bottom: 16px; box-shadow: 0 1px 4px rgba(0,0,0,0.08); overflow: hidden; }
  .pptx-slide-header { background: #f0f0f0; padding: 6px 14px; font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 1px; border-bottom: 1px solid #ddd; }
  .pptx-slide p { margin: 8px 14px; }
  .pptx-empty { color: #999; text-align: center; padding: 40px; }
  /* PDF */
  .pdf-embedded { width: 100%; height: 100%; min-height: 500px; }
  /* Common */
  p { margin: 6px 0; }
  table { margin: 8px 0; }
  hr { margin: 12px 0; }
  h1, h2, h3, h4 { margin: 12px 0 6px; }
</style>
</head>
<body>${html}</body>
</html>`;

    iframe.srcdoc = fullHtml;

    // Recalculate zoom after load
    const onLoad = () => {
      calcFitZoom();
      // Also try ResizeObserver
      const container = containerRef.current;
      if (!container) return;
      const ro = new ResizeObserver(() => calcFitZoom());
      ro.observe(container);
      setTimeout(() => ro.disconnect(), 500);
    };

    iframe.addEventListener('load', onLoad, { once: true });
    return () => {
      iframe.removeEventListener('load', onLoad);
    };
  }, [html, calcFitZoom]);

  // Apply zoom to iframe body
  useEffect(() => {
    const doc = iframeRef.current?.contentDocument;
    if (!doc?.body) return;
    (doc.body.style as any).zoom = `${zoom}`;
  }, [zoom]);

  const zoomIn = () => setZoom((z) => Math.min(z + ZOOM_STEP, ZOOM_MAX));
  const zoomOut = () => setZoom((z) => Math.max(z - ZOOM_STEP, ZOOM_MIN));
  const zoomReset = () => setZoom(fitZoomRef.current);

  return (
    <div className="preview-office-html" ref={containerRef}>
      <div className="preview-office-toolbar">
        <div className="preview-office-toolbar-left">
          <span className="preview-office-filename">{fileName}</span>
        </div>
        <div className="preview-office-toolbar-right">
          <button className="preview-office-zoom-btn" onClick={zoomOut} title="Zoom out">−</button>
          <span className="preview-office-zoom-level">{Math.round(zoom * 100)}%</span>
          <button className="preview-office-zoom-btn" onClick={zoomIn} title="Zoom in">+</button>
          <button className="preview-office-zoom-btn" onClick={zoomReset} title="Fit width">F</button>
        </div>
      </div>
      <div className="preview-office-iframe-wrapper">
        <iframe
          ref={iframeRef}
          sandbox="allow-same-origin"
          className="preview-office-iframe"
          title={fileName}
        />
      </div>
    </div>
  );
}
