package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FiscalPrinter interface — Hasar y Epson lo implementan
type FiscalPrinter interface {
	Status() (map[string]interface{}, error)
	HasPendingDocument() bool
	CancelPending() error
	PrintTicket(document map[string]interface{}) (map[string]interface{}, error)
}

// NewPrinter crea el adapter correcto según la marca
func NewPrinter(brand, ip string, port, timeout int) FiscalPrinter {
	switch brand {
	case "epson":
		return NewEpsonPrinter(ip, port, timeout)
	default:
		return NewHasarPrinter(ip, port, timeout)
	}
}

// ══════════════════════════════════════
// Hasar 2G — HTTP/JSON en puerto 7000
// ══════════════════════════════════════

type HasarPrinter struct {
	baseURL string
	client  *http.Client
}

func NewHasarPrinter(ip string, port, timeout int) *HasarPrinter {
	return &HasarPrinter{
		baseURL: fmt.Sprintf("http://%s:%d/api/v1", ip, port),
		client:  &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
}

func (p *HasarPrinter) Status() (map[string]interface{}, error) {
	resp, err := httpGet(p.client, p.baseURL+"/status")
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]interface{})
	return result, nil
}

func (p *HasarPrinter) HasPendingDocument() bool {
	status, err := p.Status()
	if err != nil {
		return false
	}
	pending, _ := status["pending_document"].(bool)
	return pending
}

func (p *HasarPrinter) CancelPending() error {
	_, err := httpPost(p.client, p.baseURL+"/fiscal/cancel", map[string]interface{}{})
	return err
}

func (p *HasarPrinter) PrintTicket(document map[string]interface{}) (map[string]interface{}, error) {
	customer, _ := document["customer"].(map[string]interface{})
	taxCondition := getString(customer, "tax_condition", "consumidor_final")

	payload := map[string]interface{}{
		"document_type": "ticket",
		"header": map[string]interface{}{
			"customer":         buildCustomer(customer),
			"document_subtype": ticketSubtype(taxCondition),
		},
		"items":    buildItems(document),
		"payments": buildPayments(document),
		"footer":   map[string]string{"text": "Gracias por su visita - Mi Tienda"},
	}

	resp, err := httpPost(p.client, p.baseURL+"/fiscal/ticket", payload)
	if err != nil {
		return nil, err
	}

	success, _ := resp["success"].(bool)
	if !success {
		errData, _ := resp["error"].(map[string]interface{})
		return nil, fmt.Errorf("%s", getString(errData, "message", "Error de impresora"))
	}

	result, _ := resp["result"].(map[string]interface{})
	return result, nil
}

// ══════════════════════════════════════
// Epson TM-T900FA — HTTP/JSON en puerto 8000
// Protocolo similar pero con estructura diferente
// ══════════════════════════════════════

type EpsonPrinter struct {
	baseURL string
	client  *http.Client
}

func NewEpsonPrinter(ip string, port, timeout int) *EpsonPrinter {
	if port == 0 || port == 7000 {
		port = 8000 // Epson default
	}
	return &EpsonPrinter{
		baseURL: fmt.Sprintf("http://%s:%d", ip, port),
		client:  &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
}

func (p *EpsonPrinter) Status() (map[string]interface{}, error) {
	resp, err := httpGet(p.client, p.baseURL+"/api/v1/printer/status")
	if err != nil {
		return nil, err
	}
	// Epson devuelve status directo
	return resp, nil
}

func (p *EpsonPrinter) HasPendingDocument() bool {
	status, err := p.Status()
	if err != nil {
		return false
	}
	// Epson usa "documentInProgress"
	pending, _ := status["documentInProgress"].(bool)
	return pending
}

func (p *EpsonPrinter) CancelPending() error {
	_, err := httpPost(p.client, p.baseURL+"/api/v1/printer/cancelDocument", map[string]interface{}{})
	return err
}

func (p *EpsonPrinter) PrintTicket(document map[string]interface{}) (map[string]interface{}, error) {
	customer, _ := document["customer"].(map[string]interface{})
	taxCondition := getString(customer, "tax_condition", "consumidor_final")

	// Epson usa estructura diferente
	payload := map[string]interface{}{
		"documentType": epsonDocType(taxCondition),
		"customer":     buildEpsonCustomer(customer),
		"items":        buildEpsonItems(document),
		"payments":     buildEpsonPayments(document),
		"footerText":   "Gracias por su visita - Mi Tienda",
	}

	resp, err := httpPost(p.client, p.baseURL+"/api/v1/printer/fiscal/ticket", payload)
	if err != nil {
		return nil, err
	}

	// Normalizar respuesta al mismo formato que Hasar
	status, _ := resp["status"].(string)
	if status == "error" {
		errMsg := getString(resp, "errorMessage", "Error de impresora Epson")
		return nil, fmt.Errorf("%s", errMsg)
	}

	result := map[string]interface{}{
		"fiscal_number": resp["documentNumber"],
		"cae":           resp["cae"],
		"point_of_sale": resp["pointOfSale"],
	}
	return result, nil
}

func buildEpsonCustomer(customer map[string]interface{}) map[string]interface{} {
	taxCondition := getString(customer, "tax_condition", "consumidor_final")
	result := map[string]interface{}{
		"ivaCondition": epsonIvaCondition(taxCondition),
	}

	name := getString(customer, "name", "")
	if name != "" && name != "Consumidor Final" {
		result["name"] = name
	}

	docNumber := getString(customer, "doc_number", "")
	if docNumber != "" {
		if dt, ok := customer["doc_type"].(float64); ok && int(dt) == 80 {
			result["documentType"] = "CUIT"
		} else {
			result["documentType"] = "DNI"
		}
		result["documentNumber"] = docNumber
	}

	return result
}

func buildEpsonItems(document map[string]interface{}) []map[string]interface{} {
	items, _ := document["items"].([]interface{})
	result := make([]map[string]interface{}, 0, len(items))

	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		desc := getString(item, "description", "Producto")
		if len(desc) > 40 {
			desc = desc[:40]
		}
		result = append(result, map[string]interface{}{
			"description": desc,
			"quantity":    getFloat(item, "qty", 1),
			"unitPrice":   getFloat(item, "unit_price", 0),
			"vatRate":     21.0,
		})
	}
	return result
}

func buildEpsonPayments(document map[string]interface{}) []map[string]interface{} {
	payments, _ := document["payments"].([]interface{})
	result := make([]map[string]interface{}, 0, len(payments))

	for _, raw := range payments {
		payment, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		method := getString(payment, "method", "cash")
		result = append(result, map[string]interface{}{
			"description": paymentLabel(method),
			"amount":      getFloat(payment, "amount", 0),
		})
	}
	return result
}

func epsonDocType(tc string) string {
	switch tc {
	case "responsable_inscripto":
		return "TICKET_FACTURA_A"
	case "monotributista":
		return "TICKET_FACTURA_C"
	default:
		return "TICKET_FACTURA_B"
	}
}

func epsonIvaCondition(tc string) string {
	switch tc {
	case "responsable_inscripto":
		return "RI"
	case "monotributista":
		return "M"
	case "exento":
		return "EX"
	default:
		return "CF"
	}
}

// ══════════════════════════════════════
// Shared helpers
// ══════════════════════════════════════

func buildCustomer(customer map[string]interface{}) map[string]interface{} {
	taxCondition := getString(customer, "tax_condition", "consumidor_final")
	result := map[string]interface{}{
		"iva_condition": ivaCondition(taxCondition),
	}

	name := getString(customer, "name", "")
	if name != "" && name != "Consumidor Final" {
		result["name"] = name
	}

	docNumber := getString(customer, "doc_number", "")
	if docNumber != "" {
		docType := "DNI"
		if dt, ok := customer["doc_type"].(float64); ok && int(dt) == 80 {
			docType = "CUIT"
		}
		result["identification"] = map[string]string{"type": docType, "number": docNumber}
	}
	return result
}

func buildItems(document map[string]interface{}) []map[string]interface{} {
	items, _ := document["items"].([]interface{})
	result := make([]map[string]interface{}, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		desc := getString(item, "description", "Producto")
		if len(desc) > 40 {
			desc = desc[:40]
		}
		result = append(result, map[string]interface{}{
			"description": desc,
			"quantity":    getFloat(item, "qty", 1),
			"unit_price":  getFloat(item, "unit_price", 0),
			"vat_rate":    21.0,
			"item_type":   "SALE",
		})
	}
	return result
}

func buildPayments(document map[string]interface{}) []map[string]interface{} {
	payments, _ := document["payments"].([]interface{})
	result := make([]map[string]interface{}, 0, len(payments))
	for _, raw := range payments {
		payment, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		method := getString(payment, "method", "cash")
		result = append(result, map[string]interface{}{
			"description":  paymentLabel(method),
			"amount":       getFloat(payment, "amount", 0),
			"payment_type": paymentType(method),
		})
	}
	return result
}

// HTTP helpers

func httpGet(client *http.Client, url string) (map[string]interface{}, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()
	return parseBody(resp)
}

func httpPost(client *http.Client, url string, body interface{}) (map[string]interface{}, error) {
	jsonData, _ := json.Marshal(body)
	resp, err := client.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()
	return parseBody(resp)
}

func parseBody(resp *http.Response) (map[string]interface{}, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
	}
	return result, nil
}

// Value helpers

func ivaCondition(tc string) string {
	m := map[string]string{
		"responsable_inscripto": "RESPONSABLE_INSCRIPTO",
		"monotributista":        "MONOTRIBUTISTA",
		"exento":                "EXENTO",
	}
	if v, ok := m[tc]; ok {
		return v
	}
	return "CONSUMIDOR_FINAL"
}

func ticketSubtype(tc string) string {
	m := map[string]string{"responsable_inscripto": "TICKET_A", "monotributista": "TICKET_C"}
	if v, ok := m[tc]; ok {
		return v
	}
	return "TICKET_B"
}

func paymentType(method string) string {
	m := map[string]string{"cash": "CASH", "credit_card": "CREDIT_CARD", "debit_card": "DEBIT_CARD"}
	if v, ok := m[method]; ok {
		return v
	}
	return "OTHER"
}

func paymentLabel(method string) string {
	m := map[string]string{
		"cash": "Efectivo", "transfer": "Transferencia", "mercado_pago": "MercadoPago",
		"credit_card": "Tarjeta credito", "debit_card": "Tarjeta debito",
	}
	if v, ok := m[method]; ok {
		return v
	}
	return "Otros"
}

func getString(m map[string]interface{}, key, fallback string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return fallback
}

func getFloat(m map[string]interface{}, key string, fallback float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return fallback
}
