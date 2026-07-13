package main

import (
	"archive/zip"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ── OOXML internal types (only fields we care about) ──────────

type wDoc struct {
	XMLName xml.Name `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main document"`
	Body    wBody    `xml:"body"`
}

type wBody struct {
	Paragraphs []wParagraph `xml:"p"`
	Tables     []wTable     `xml:"tbl"`
}

type wParagraph struct {
	Runs       []wRun       `xml:"r"`
	Hyperlinks []wHyperlink `xml:"hyperlink"`
	PPr        *wPPr        `xml:"pPr"`
}

type wPPr struct {
	Justification *wJustification `xml:"jc"`
}

type wJustification struct {
	Val string `xml:"val,attr"`
}

type wRun struct {
	Text     string `xml:"t"`
	RunStyle *struct {
		Bold      *string `xml:"b"`
		Italic    *string `xml:"i"`
		Underline *string `xml:"u"`
		Sz        *string `xml:"sz"`
	} `xml:"rPr"`
}

type wHyperlink struct {
	Runs []wRun `xml:"r"`
	ID   string `xml:"id,attr"`
}

type wTable struct {
	Rows []wTableRow `xml:"tr"`
}

type wTableRow struct {
	Cells []wTableCell `xml:"tc"`
}

type wTableCell struct {
	Paragraphs []wParagraph `xml:"p"`
}

type wRelationships struct {
	XMLName xml.Name    `xml:"Relationships"`
	Rels    []wRelation `xml:"Relationship"`
}

type wRelation struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
	Type   string `xml:"Type,attr"`
}

// ── XLSX types ────────────────────────────────────────────────

type xWorkbook struct {
	XMLName xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main workbook"`
	Sheets  xSheets  `xml:"sheets"`
}

type xSheets struct {
	Sheet []xSheet `xml:"sheet"`
}

type xSheet struct {
	Name string `xml:"name,attr"`
	RID  string `xml:"id,attr"`
}

type xSharedStrings struct {
	XMLName xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main sst"`
	Items   []xSI    `xml:"si"`
}

type xSI struct {
	Text string `xml:"t"`
}

type xWorksheet struct {
	XMLName   xml.Name   `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main worksheet"`
	SheetData xSheetData `xml:"sheetData"`
	Cols      *xCols     `xml:"cols"`
}

type xCols struct {
	Col []xCol `xml:"col"`
}

type xCol struct {
	Min     string `xml:"min,attr"`
	Max     string `xml:"max,attr"`
	Width   string `xml:"width,attr"`
	BestFit string `xml:"bestFit,attr"`
}

type xSheetData struct {
	Rows []xRow `xml:"row"`
}

type xRow struct {
	Cells []xCell `xml:"c"`
}

type xCell struct {
	Ref      string `xml:"r,attr"`
	Type     string `xml:"t,attr"` // "s" = shared string, "str" = inline
	StyleIdx string `xml:"s,attr"`
	Value    string `xml:"v"`
}

// ── PPTX types ────────────────────────────────────────────────

type pPresentation struct {
	XMLName  xml.Name  `xml:"http://schemas.openxmlformats.org/presentationml/2006/main presentation"`
	SlideIDs pSlideIDs `xml:"sldIdLst"`
}

type pSlideIDs struct {
	IDs []pSlideID `xml:"sldId"`
}

type pSlideID struct {
	ID  string `xml:"id,attr"`
	RID string `xml:"r:id,attr"`
}

type pSlide struct {
	XMLName       xml.Name        `xml:"http://schemas.openxmlformats.org/presentationml/2006/main sld"`
	CommonSldData *pCommonSldData `xml:"cSld"`
}

type pCommonSldData struct {
	ShapeTree pShapeTree `xml:"spTree"`
}

type pShapeTree struct {
	Shapes      []pShape      `xml:"sp"`
	GroupShapes []pGroupShape `xml:"grpSp"`
}

type pShape struct {
	TextBody *pTextBody `xml:"txBody"`
	Name     string     `xml:"cNvPr>name,attr"`
}

type pGroupShape struct {
	Shapes []pShape `xml:"sp"`
}

type pTextBody struct {
	Paragraphs []pParagraph `xml:"p"`
}

type pParagraph struct {
	Runs      []pRun    `xml:"r"`
	LineBreak *struct{} `xml:"br"`
	Level     string    `xml:"lvl,attr"`
}

type pRun struct {
	Text     string `xml:"t"`
	RunStyle *struct {
		Sz        *string `xml:"sz"`
		Bold      *string `xml:"b"`
		Italic    *string `xml:"i"`
		Underline *string `xml:"u"`
		Latin     *struct {
			Typeface string `xml:"typeface,attr"`
		} `xml:"latin"`
	} `xml:"rPr"`
}

// ── RenderToHTML ──────────────────────────────────────────────

// RenderToHTML converts an Office document (docx/xlsx/pptx) to an HTML string.
// For PDFs, it wraps the binary data as a base64-embedded PDF viewer.
func (a *App) RenderToHTML(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".docx":
		return renderDocxToHTML(filePath)
	case ".xlsx":
		return renderXlsxToHTML(filePath)
	case ".pptx":
		return renderPptxToHTML(filePath)
	case ".pdf":
		return renderPdfToHTML(filePath)
	default:
		return "", fmt.Errorf("unsupported format: %s", ext)
	}
}

// ── DOCX → HTML ───────────────────────────────────────────────

func renderDocxToHTML(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	// Read relationships for hyperlinks
	rels := readXMLFile[wRelationships](r, "word/_rels/document.xml.rels")
	_ = rels // could use for hyperlink resolution

	doc := readXMLFile[wDoc](r, "word/document.xml")
	if doc == nil {
		return "", fmt.Errorf("word/document.xml not found in docx")
	}

	var parts []string
	parts = append(parts, `<div class="docx-rendered">`)

	// Process tables first (they might appear inline but we handle them by position)
	// In practice, docx tables are contained within the body in order.
	// We iterate over body children in document order.
	// For simplicity, iterate paragraphs and tables interleaved.
	paraIdx := 0
	tableIdx := 0
	for paraIdx < len(doc.Body.Paragraphs) || tableIdx < len(doc.Body.Tables) {
		// Simple heuristic: process paragraphs first
		if paraIdx < len(doc.Body.Paragraphs) {
			parts = append(parts, paragraphToHTML(doc.Body.Paragraphs[paraIdx]))
			paraIdx++
		}
		if tableIdx < len(doc.Body.Tables) {
			parts = append(parts, tableToHTML(doc.Body.Tables[tableIdx]))
			tableIdx++
		}
	}

	parts = append(parts, `</div>`)
	return strings.Join(parts, "\n"), nil
}

func paragraphToHTML(p wParagraph) string {
	if len(p.Runs) == 0 && len(p.Hyperlinks) == 0 {
		return `<p><br/></p>`
	}

	align := ""
	if p.PPr != nil && p.PPr.Justification != nil {
		switch p.PPr.Justification.Val {
		case "center":
			align = ` style="text-align:center"`
		case "right":
			align = ` style="text-align:right"`
		case "both":
			align = ` style="text-align:justify"`
		}
	}

	var content strings.Builder
	for _, run := range p.Runs {
		closeTag := ""
		if run.RunStyle != nil {
			if run.RunStyle.Bold != nil {
				content.WriteString("<strong>")
				closeTag = "</strong>" + closeTag
			}
			if run.RunStyle.Italic != nil {
				content.WriteString("<em>")
				closeTag = "</em>" + closeTag
			}
		}
		content.WriteString(escapeHTML(run.Text))
		content.WriteString(closeTag)
	}

	// Hyperlinks
	for _, hl := range p.Hyperlinks {
		for _, run := range hl.Runs {
			content.WriteString(escapeHTML(run.Text))
		}
	}

	text := content.String()
	if strings.TrimSpace(text) == "" {
		return `<p><br/></p>`
	}

	return fmt.Sprintf(`<p%s>%s</p>`, align, text)
}

func tableToHTML(t wTable) string {
	var b strings.Builder
	b.WriteString(`<table class="docx-table"><tbody>`)
	for _, row := range t.Rows {
		b.WriteString(`<tr>`)
		for _, cell := range row.Cells {
			b.WriteString(`<td>`)
			for _, p := range cell.Paragraphs {
				b.WriteString(paragraphToHTML(p))
			}
			b.WriteString(`</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// ── XLSX → HTML ───────────────────────────────────────────────

func renderXlsxToHTML(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer r.Close()

	// Shared strings
	ss := readXMLFile[xSharedStrings](r, "xl/sharedStrings.xml")

	// Workbook
	wb := readXMLFile[xWorkbook](r, "xl/workbook.xml")

	var parts []string
	parts = append(parts, `<div class="xlsx-rendered">`)

	if wb != nil {
		for i, sheet := range wb.Sheets.Sheet {
			if i > 0 {
				parts = append(parts, `<hr class="xlsx-sheet-sep"/>`)
			}
			parts = append(parts, fmt.Sprintf(`<h3 class="xlsx-sheet-name">%s</h3>`, escapeHTML(sheet.Name)))

			// Map RID to file path
			rels := readXMLFile[wRelationships](r, "xl/_rels/workbook.xml.rels")
			sheetFile := ""
			if rels != nil {
				for _, rel := range rels.Rels {
					if rel.ID == sheet.RID {
						sheetFile = "xl/" + rel.Target
						break
					}
				}
			}
			if sheetFile == "" {
				sheetFile = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
			}

			ws := readXMLFile[xWorksheet](r, sheetFile)
			if ws == nil {
				continue
			}

			parts = append(parts, `<table class="xlsx-table"><tbody>`)
			for _, row := range ws.SheetData.Rows {
				parts = append(parts, `<tr>`)
				for _, cell := range row.Cells {
					val := cell.Value
					if cell.Type == "s" && ss != nil {
						idx, err := strconv.Atoi(val)
						if err == nil && idx < len(ss.Items) {
							val = ss.Items[idx].Text
						}
					}
					parts = append(parts, fmt.Sprintf(`<td>%s</td>`, escapeHTML(val)))
				}
				parts = append(parts, `</tr>`)
			}
			parts = append(parts, `</tbody></table>`)
		}
	}

	parts = append(parts, `</div>`)
	return strings.Join(parts, "\n"), nil
}

// ── PPTX → HTML ───────────────────────────────────────────────

func renderPptxToHTML(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	rels := readXMLFile[wRelationships](r, "ppt/_rels/presentation.xml.rels")
	pres := readXMLFile[pPresentation](r, "ppt/presentation.xml")

	var parts []string
	parts = append(parts, `<div class="pptx-rendered">`)

	if pres != nil && rels != nil {
		for i, sid := range pres.SlideIDs.IDs {
			slideFile := ""
			for _, rel := range rels.Rels {
				if rel.ID == sid.RID {
					slideFile = "ppt/" + rel.Target
					break
				}
			}
			if slideFile == "" {
				continue
			}

			sl := readXMLFile[pSlide](r, slideFile)
			if sl == nil {
				continue
			}

			parts = append(parts, fmt.Sprintf(`<div class="pptx-slide"><div class="pptx-slide-header">Slide %d</div>`, i+1))

			// Extract shapes
			var shapes []pShape
			if sl.CommonSldData != nil {
				shapes = append(shapes, sl.CommonSldData.ShapeTree.Shapes...)
				for _, gs := range sl.CommonSldData.ShapeTree.GroupShapes {
					shapes = append(shapes, gs.Shapes...)
				}
			}

			for _, shape := range shapes {
				if shape.TextBody == nil {
					continue
				}
				for _, p := range shape.TextBody.Paragraphs {
					var line strings.Builder
					for _, run := range p.Runs {
						end := ""
						if run.RunStyle != nil {
							if run.RunStyle.Bold != nil {
								line.WriteString("<strong>")
								end = "</strong>" + end
							}
							if run.RunStyle.Italic != nil {
								line.WriteString("<em>")
								end = "</em>" + end
							}
						}
						line.WriteString(escapeHTML(run.Text))
						line.WriteString(end)
					}
					if line.Len() > 0 {
						parts = append(parts, fmt.Sprintf(`<p>%s</p>`, line.String()))
					} else {
						parts = append(parts, `<p><br/></p>`)
					}
				}
			}

			parts = append(parts, `</div>`)
		}
	}

	if len(parts) == 1 {
		parts = append(parts, `<p class="pptx-empty">No slides could be extracted.</p>`)
	}

	parts = append(parts, `</div>`)
	return strings.Join(parts, "\n"), nil
}

// ── PDF → HTML (embed as base64) ──────────────────────────────

func renderPdfToHTML(path string) (string, error) {
	data, err := readFileBytes(path)
	if err != nil {
		return "", err
	}
	// Return the base64-encoded PDF data; the frontend will embed it directly
	return base64.StdEncoding.EncodeToString(data), nil
}

// ── helpers ───────────────────────────────────────────────────

func readXMLFile[T any](r *zip.ReadCloser, name string) *T {
	for _, f := range r.File {
		normalized := strings.ReplaceAll(f.Name, "\\", "/")
		// Use HasSuffix for robust matching across different OOXML generators
		if strings.HasSuffix(normalized, name) {
			rc, err := f.Open()
			if err != nil {
				return nil
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return nil
			}
			var v T
			if err := xml.Unmarshal(data, &v); err != nil {
				return nil
			}
			return &v
		}
	}
	return nil
}

func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
