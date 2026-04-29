package main

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// ESC/POS commands
var (
	escInit      = []byte{0x1B, 0x40}             // ESC @
	escCenter    = []byte{0x1B, 0x61, 1}          // ESC a 1
	escLeft      = []byte{0x1B, 0x61, 0}          // ESC a 0
	escBoldOn    = []byte{0x1B, 0x45, 1}          // ESC E 1
	escBoldOff   = []byte{0x1B, 0x45, 0}          // ESC E 0
	escDoubleOn  = []byte{0x1B, 0x21, 0x30}       // double height + width
	escDoubleOff = []byte{0x1B, 0x21, 0x00}       // normal
	escFullCut   = []byte{0x1D, 0x56, 0x00}       // GS V 0
	escNewLine   = []byte{0x0A}
	escDrawerKick = []byte{0x1B, 0x70, 0x00, 0x19, 0xFA}
)

type EscPosLAN struct {
	ip            string
	port          int
	paperWidthMM  int
	charsPerLine  int
	autoCut       bool
}

func NewEscPosLAN(ip string, port, paperWidthMM, charsPerLine int, autoCut bool) *EscPosLAN {
	if port == 0 {
		port = 9100
	}
	if charsPerLine == 0 {
		if paperWidthMM == 58 {
			charsPerLine = 32
		} else {
			charsPerLine = 48
		}
	}
	return &EscPosLAN{
		ip:           ip,
		port:         port,
		paperWidthMM: paperWidthMM,
		charsPerLine: charsPerLine,
		autoCut:      autoCut,
	}
}

func (p *EscPosLAN) Kind() string { return "thermal" }

func (p *EscPosLAN) Status() (map[string]interface{}, error) {
	// Try to open a connection to verify the printer is reachable
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.ip, p.port), 3*time.Second)
	if err != nil {
		return map[string]interface{}{"online": false, "error": err.Error()}, nil
	}
	conn.Close()
	return map[string]interface{}{"online": true}, nil
}

func (p *EscPosLAN) Print(job map[string]interface{}) (map[string]interface{}, error) {
	data := buildEscPosBytes(job, p.charsPerLine, p.autoCut)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.ip, p.port), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))

	// Phase 1: Wake up printer with init + sacrificial blank lines.
	// Global printers eat ~20 bytes on first write after idle.
	var wake bytes.Buffer
	for i := 0; i < 32; i++ {
		wake.WriteByte(0x00)
	}
	wake.Write([]byte{0x1B, 0x40}) // ESC @ init
	for i := 0; i < 5; i++ {
		wake.WriteByte(0x0A) // LF - sacrificial blank lines
	}
	wake.Write([]byte{0x1B, 0x40}) // ESC @ re-init (clears the blank lines)
	conn.Write(wake.Bytes())
	time.Sleep(1000 * time.Millisecond)

	// Phase 2: Send actual content in chunks
	chunkSize := 256
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if _, err := conn.Write(data[i:end]); err != nil {
			return nil, fmt.Errorf("write failed: %w", err)
		}
		if end < len(data) {
			time.Sleep(30 * time.Millisecond)
		}
	}

	return map[string]interface{}{"printed": true}, nil
}

// buildEscPosBytes generates a printable comanda/ticket for thermal printers.
// Supports two modes:
//   - Standard comanda: header + items + total + footer
//   - Fiscal ticket (format="fiscal"): full AFIP format with sections
func buildEscPosBytes(job map[string]interface{}, charsPerLine int, autoCut bool) []byte {
	var buf bytes.Buffer

	// Sacrificial bytes: if the printer eats bytes from the content
	// stream, these get eaten instead of the real data.
	for i := 0; i < 64; i++ {
		buf.WriteByte(0x00)
	}
	buf.Write(escInit)
	buf.Write(escDoubleOff)
	buf.Write(escBoldOff)

	paperWidthMM, _ := job["paper_width_mm"].(float64)
	if int(paperWidthMM) == 58 {
		buf.Write([]byte{0x1D, 0x57, 0x80, 0x01})
	}

	comanda, _ := job["comanda"].(map[string]interface{})
	if comanda == nil {
		return buf.Bytes()
	}

	format, _ := comanda["format"].(string)

	if format == "fiscal" {
		buildFiscalTicket(&buf, comanda, charsPerLine)
	} else {
		buildStandardComanda(&buf, job, comanda, charsPerLine)
	}

	for i := 0; i < 6; i++ {
		buf.Write(escNewLine)
	}
	if autoCut {
		buf.Write(escFullCut)
	}
	if openDrawer, _ := job["open_drawer"].(bool); openDrawer {
		buf.Write(escDrawerKick)
	}

	return buf.Bytes()
}

func buildFiscalTicket(buf *bytes.Buffer, comanda map[string]interface{}, cpl int) {
	sep := strings.Repeat("-", cpl)

	// Helper to write centered lines
	writeCentered := func(text string) {
		buf.Write(escCenter)
		for _, line := range strings.Split(text, "\n") {
			if line != "" {
				buf.WriteString(line)
			}
			buf.Write(escNewLine)
		}
	}

	writeLeft := func(text string) {
		buf.Write(escLeft)
		for _, line := range strings.Split(text, "\n") {
			if line != "" {
				buf.WriteString(line)
			}
			buf.Write(escNewLine)
		}
	}

	writeRow := func(left, right string) {
		buf.Write(escLeft)
		pad := cpl - len(left) - len(right)
		if pad < 1 {
			pad = 1
		}
		buf.WriteString(left)
		buf.WriteString(strings.Repeat(" ", pad))
		buf.WriteString(right)
		buf.Write(escNewLine)
	}

	// ═══ SECTION 1: Emisor ═══
	buf.Write(escCenter)
	if name, _ := comanda["emisor_name"].(string); name != "" {
		buf.Write(escBoldOn)
		buf.WriteString(name)
		buf.Write(escBoldOff)
		buf.Write(escNewLine)
	}
	if info, _ := comanda["emisor_info"].(string); info != "" {
		writeCentered(info)
	}

	buf.WriteString(sep)
	buf.Write(escNewLine)

	// ═══ SECTION 2: Receptor ═══
	if receptor, _ := comanda["receptor"].(string); receptor != "" {
		writeCentered(receptor)
		buf.WriteString(sep)
		buf.Write(escNewLine)
	}

	// ═══ SECTION 3: Comprobante ═══
	if voucher, _ := comanda["voucher_type"].(string); voucher != "" {
		buf.Write(escCenter)
		buf.Write(escDoubleOn)
		buf.WriteString(strings.ToUpper(voucher))
		buf.Write(escDoubleOff)
		buf.Write(escNewLine)
	}
	if voucherInfo, _ := comanda["voucher_info"].(string); voucherInfo != "" {
		writeLeft(voucherInfo)
	}

	buf.Write(escLeft)
	buf.WriteString(sep)
	buf.Write(escNewLine)

	// ═══ SECTION 4: Items ═══
	items, _ := comanda["items"].([]interface{})
	buf.Write(escLeft)
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		subtotalStr, _ := item["subtotal_fmt"].(string)
		detail, _ := item["detail"].(string)

		// Line 1: name + total
		buf.Write(escBoldOn)
		writeRow(name, subtotalStr)
		buf.Write(escBoldOff)
		// Line 2: qty x price (IVA%)
		if detail != "" {
			writeLeft(detail)
		}
	}

	buf.WriteString(sep)
	buf.Write(escNewLine)

	// ═══ SECTION 5: Total ═══
	if totalFmt, ok := comanda["total_fmt"].(string); ok && totalFmt != "" {
		buf.Write(escBoldOn)
		writeRow("TOTAL", totalFmt)
		buf.Write(escBoldOff)
	}

	buf.WriteString(sep)
	buf.Write(escNewLine)

	// ═══ SECTION 6: Rows (transparency, payments, etc.) ═══
	if rows, ok := comanda["detail_rows"].([]interface{}); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			left, _ := row["l"].(string)
			right, _ := row["r"].(string)
			isSep, _ := row["sep"].(bool)
			isBold, _ := row["bold"].(bool)

			if isSep {
				buf.Write(escLeft)
				buf.WriteString(sep)
				buf.Write(escNewLine)
			} else if right != "" {
				if isBold {
					buf.Write(escBoldOn)
				}
				writeRow(left, right)
				if isBold {
					buf.Write(escBoldOff)
				}
			} else if left != "" {
				writeLeft(left)
			}
		}
	}

	// ═══ SECTION 8: Footer ═══
	if footer, _ := comanda["footer"].(string); footer != "" {
		buf.WriteString(sep)
		buf.Write(escNewLine)
		writeLeft(footer)
	}

	// ═══ SECTION 9: QR ═══
	if qrURL, _ := comanda["qr_url"].(string); qrURL != "" {
		buf.Write(escCenter)
		buf.Write(escNewLine)
		printQRCode(buf, qrURL)
		buf.Write(escNewLine)
	}

	// ═══ SECTION 10: Legal ═══
	buf.Write(escCenter)
	buf.Write(escNewLine)
	buf.WriteString("Comprobante electronico")
	buf.Write(escNewLine)
}

func printQRCode(buf *bytes.Buffer, url string) {
	qrData := []byte(url)
	dataLen := len(qrData) + 3

	// QR model 2
	buf.Write([]byte{0x1D, 0x28, 0x6B, 0x04, 0x00, 0x31, 0x41, 0x32, 0x00})
	// Module size 4
	buf.Write([]byte{0x1D, 0x28, 0x6B, 0x03, 0x00, 0x31, 0x43, 0x04})
	// Error correction L
	buf.Write([]byte{0x1D, 0x28, 0x6B, 0x03, 0x00, 0x31, 0x45, 0x30})
	// Store data
	buf.Write([]byte{0x1D, 0x28, 0x6B, byte(dataLen & 0xFF), byte((dataLen >> 8) & 0xFF), 0x31, 0x50, 0x30})
	buf.Write(qrData)
	// Print
	buf.Write([]byte{0x1D, 0x28, 0x6B, 0x03, 0x00, 0x31, 0x51, 0x30})
}

func buildStandardComanda(buf *bytes.Buffer, job map[string]interface{}, comanda map[string]interface{}, charsPerLine int) {
	header, _ := comanda["header"].(map[string]interface{})
	items, _ := comanda["items"].([]interface{})
	footer, _ := comanda["footer"].(string)

	// Header (centered, bold)
	buf.Write(escCenter)
	buf.Write(escBoldOn)
	if name, ok := header["club_name"].(string); ok && name != "" {
		buf.WriteString(name)
		buf.Write(escNewLine)
	}
	buf.Write(escBoldOff)

	if addr, ok := header["club_address"].(string); ok && addr != "" {
		buf.WriteString(addr)
		buf.Write(escNewLine)
	}

	if role, ok := header["role"].(string); ok && role != "" {
		buf.Write(escDoubleOn)
		buf.WriteString(strings.ToUpper(role))
		buf.Write(escDoubleOff)
		buf.Write(escNewLine)
	}

	if t, ok := header["time"].(string); ok {
		buf.WriteString(t)
		buf.Write(escNewLine)
	}

	if saleID := header["sale_id"]; saleID != nil {
		buf.WriteString(fmt.Sprintf("Venta #%v", saleID))
		buf.Write(escNewLine)
	}

	if cashier, ok := header["cashier"].(string); ok && cashier != "" {
		buf.WriteString(fmt.Sprintf("Cajero: %s", cashier))
		buf.Write(escNewLine)
	}

	buf.Write(escNewLine)
	buf.WriteString(strings.Repeat("-", charsPerLine))
	buf.Write(escNewLine)

	format, _ := comanda["format"].(string)
	isKitchen := format == "kitchen"

	buf.Write(escLeft)

	if isKitchen {
		for _, raw := range items {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			qty := getFloat(item, "qty", 1)
			name, _ := item["name"].(string)
			buf.Write(escDoubleOn)
			buf.WriteString(fmt.Sprintf("%dx %s", int(qty), name))
			buf.Write(escDoubleOff)
			buf.Write(escNewLine)
			// Nota por item (ej: "sin queso"). Crítica para cocina, la
			// imprimimos en negrita para que no pase desapercibida.
			if note, _ := item["notes"].(string); strings.TrimSpace(note) != "" {
				buf.Write(escBoldOn)
				writeWrappedLines(buf, "  > "+strings.TrimSpace(note), charsPerLine)
				buf.Write(escBoldOff)
			}
		}
	} else {
		for _, raw := range items {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			qty := getFloat(item, "qty", 1)
			name, _ := item["name"].(string)
			subtotal := getFloat(item, "subtotal", 0)

			desc := fmt.Sprintf("%dx %s", int(qty), name)
			price := fmt.Sprintf("$%s", formatMoney(subtotal))
			padding := charsPerLine - len(desc) - len(price)
			if padding < 1 {
				padding = 1
			}
			buf.Write(escBoldOn)
			buf.WriteString(desc)
			buf.Write(escBoldOff)
			buf.WriteString(strings.Repeat(" ", padding))
			buf.WriteString(price)
			buf.Write(escNewLine)
			// Nota por item — bajo el producto, indentada.
			if note, _ := item["notes"].(string); strings.TrimSpace(note) != "" {
				writeWrappedLines(buf, "  > "+strings.TrimSpace(note), charsPerLine)
			}
		}

		buf.WriteString(strings.Repeat("-", charsPerLine))
		buf.Write(escNewLine)

		// Subtotal + adjustments (promos / payment discounts)
		adjustments, _ := comanda["adjustments"].([]interface{})
		if len(adjustments) > 0 {
			subtotal := getFloat(comanda, "subtotal", 0)
			subtotalStr := fmt.Sprintf("$%s", formatMoney(subtotal))
			label := "Subtotal"
			pad := charsPerLine - len(label) - len(subtotalStr)
			if pad < 1 {
				pad = 1
			}
			buf.WriteString(label)
			buf.WriteString(strings.Repeat(" ", pad))
			buf.WriteString(subtotalStr)
			buf.Write(escNewLine)

			for _, raw := range adjustments {
				adj, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				adjLabel, _ := adj["label"].(string)
				adjAmount := getFloat(adj, "amount", 0)
				if adjLabel == "" {
					continue
				}
				adjStr := fmt.Sprintf("-$%s", formatMoney(adjAmount))
				pad := charsPerLine - len(adjLabel) - len(adjStr)
				if pad < 1 {
					pad = 1
				}
				buf.WriteString(adjLabel)
				buf.WriteString(strings.Repeat(" ", pad))
				buf.WriteString(adjStr)
				buf.Write(escNewLine)
			}
			buf.WriteString(strings.Repeat("-", charsPerLine))
			buf.Write(escNewLine)
		}

		total := comanda["total"]
		if total != nil {
			totalStr := fmt.Sprintf("$%s", formatMoney(getFloat(comanda, "total", 0)))
			label := "TOTAL"
			pad := charsPerLine - len(label) - len(totalStr)
			if pad < 1 {
				pad = 1
			}
			buf.Write(escBoldOn)
			buf.WriteString(label)
			buf.WriteString(strings.Repeat(" ", pad))
			buf.WriteString(totalStr)
			buf.Write(escBoldOff)
			buf.Write(escNewLine)
		}

		if pm, ok := comanda["payment_method"].(string); ok && pm != "" {
			buf.WriteString(fmt.Sprintf("Pago: %s", pm))
			buf.Write(escNewLine)
		}
	}

	buf.Write(escNewLine)
	buf.WriteString(strings.Repeat("-", charsPerLine))
	buf.Write(escNewLine)

	// Nota general de la comanda (ej: "para Juan, mesa 3"). Va antes del
	// footer/aviso fiscal para que cocina/cajero la vean junto a los items.
	if note, _ := comanda["notes"].(string); strings.TrimSpace(note) != "" {
		buf.Write(escLeft)
		buf.Write(escBoldOn)
		buf.WriteString("Nota:")
		buf.Write(escBoldOff)
		buf.Write(escNewLine)
		writeWrappedLines(buf, strings.TrimSpace(note), charsPerLine)
		buf.WriteString(strings.Repeat("-", charsPerLine))
		buf.Write(escNewLine)
	}

	if footer != "" {
		buf.Write(escCenter)
		for _, line := range strings.Split(footer, "\n") {
			buf.WriteString(line)
			buf.Write(escNewLine)
		}
	}

	skipFiscal, _ := comanda["skip_fiscal_notice"].(bool)
	if !skipFiscal {
		buf.Write(escCenter)
		buf.Write(escNewLine)
		buf.WriteString("No valido como factura")
		buf.Write(escNewLine)
	}
}

// writeWrappedLines parte un texto en líneas de hasta charsPerLine,
// respetando palabras cuando se puede, y emite cada línea seguida de \n.
// Usado para notas (per-item y generales) en comandas térmicas.
func writeWrappedLines(buf *bytes.Buffer, text string, charsPerLine int) {
	if charsPerLine <= 0 {
		buf.WriteString(text)
		buf.Write(escNewLine)
		return
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " ")
		if line == "" {
			buf.Write(escNewLine)
			continue
		}
		for len(line) > charsPerLine {
			cut := charsPerLine
			// Intentar cortar en el último espacio para no romper palabras.
			if sp := strings.LastIndex(line[:charsPerLine], " "); sp > charsPerLine/2 {
				cut = sp
			}
			buf.WriteString(line[:cut])
			buf.Write(escNewLine)
			line = strings.TrimLeft(line[cut:], " ")
		}
		buf.WriteString(line)
		buf.Write(escNewLine)
	}
}

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, '.')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// formatMoney renderea un valor con centavos (si los tiene).
//
//	5000.0  -> "5.000"
//	5000.5  -> "5.000,50"
//	0.5     -> "0,50"
func formatMoney(f float64) string {
	rounded := math.Round(math.Abs(f)*100) / 100
	intPart := int(math.Floor(rounded))
	centavos := int(math.Round((rounded - float64(intPart)) * 100))
	if centavos == 0 {
		return formatNumber(intPart)
	}
	return fmt.Sprintf("%s,%02d", formatNumber(intPart), centavos)
}
