package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Known printer ports and what they mean
var scanPorts = []struct {
	port  int
	kind  string
	brand string
}{
	{9100, "thermal", ""},       // ESC/POS (generic thermal)
	{7000, "fiscal", "hasar"},   // Hasar fiscal
	{8000, "fiscal", "epson"},   // Epson fiscal
}

type LANDevice struct {
	IP    string
	Port  int
	Kind  string
	Brand string
	Label string
}

// ScanLANPrinters scans the local /24 subnet for known printer ports.
// Returns a list of discovered devices (typically completes in 2-4 seconds).
func ScanLANPrinters() []LANDevice {
	localIP := getLocalIP()
	if localIP == "" {
		log.Println("No se pudo determinar la IP local")
		return nil
	}

	subnet := getSubnet(localIP)
	log.Printf("Escaneando red %s.0/24 en busca de impresoras...", subnet)

	var devices []LANDevice
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Scan all 254 hosts in parallel with a semaphore to limit concurrency
	sem := make(chan struct{}, 50) // max 50 concurrent connections

	for i := 1; i <= 254; i++ {
		ip := fmt.Sprintf("%s.%d", subnet, i)
		if ip == localIP {
			continue
		}

		for _, sp := range scanPorts {
			wg.Add(1)
			sem <- struct{}{}
			go func(ip string, port int, kind, brand string) {
				defer wg.Done()
				defer func() { <-sem }()

				addr := fmt.Sprintf("%s:%d", ip, port)
				conn, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
				if err != nil {
					return
				}
				conn.Close()

				label := fmt.Sprintf("%s:%d", ip, port)
				switch {
				case brand == "hasar":
					label = fmt.Sprintf("Hasar Fiscal (%s)", ip)
				case brand == "epson":
					label = fmt.Sprintf("Epson Fiscal (%s)", ip)
				case kind == "thermal":
					label = fmt.Sprintf("Impresora LAN (%s)", ip)
				}

				mu.Lock()
				devices = append(devices, LANDevice{
					IP: ip, Port: port, Kind: kind, Brand: brand, Label: label,
				})
				mu.Unlock()
			}(ip, sp.port, sp.kind, sp.brand)
		}
	}

	wg.Wait()
	return devices
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}

func getSubnet(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	p := parsed.To4()
	return fmt.Sprintf("%d.%d.%d", p[0], p[1], p[2])
}
