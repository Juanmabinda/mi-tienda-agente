# CanchaYa Print Agent

Agente local que conecta CanchaYa con impresoras del club:
- **Térmicas LAN** (ESC/POS): comanderas en cocina, barra, mostrador
- **Térmicas USB** (ESC/POS): comanderas conectadas por cable
- **Fiscales LAN**: Hasar 2G, Epson TM-T900FA

Se distribuye como un ejecutable único — no requiere instalar nada extra.

## Descargar

Descargá desde [Releases](https://github.com/Juanmabinda/canchaya-print-agent/releases/latest):

| Plataforma | Archivo | USB | LAN |
|---|---|---|---|
| Mac | `canchaya-print-mac.zip` (.app firmada y notarizada) | ✅ via CUPS | ✅ |
| Windows | `canchaya-print-windows.exe` | ✅ via Print Spooler | ✅ |

## Instalar

1. **Descargar** el agente desde el link de arriba
2. **Abrir** el ejecutable (Mac: descomprimir y doble click en la .app)
3. **Parear**: el agente muestra un código de 6 caracteres en pantalla
4. En CanchaYa → **Configuración → Impresoras** → pegar el código en "Conectar agente"
5. Listo — el agente guarda el token y se configura para arrancar solo con la PC

No hace falta copiar tokens ni archivos. El código se ingresa una sola vez.

## Detección automática

Al conectarse, el agente escanea:
- **USB**: impresoras instaladas en el sistema (CUPS en Mac, Print Spooler en Windows)
- **LAN**: red local en puertos 9100 (ESC/POS), 7000 (Hasar), 8000 (Epson)

Las impresoras detectadas aparecen automáticamente en CanchaYa con un botón "Usar" para darlas de alta.

## Múltiples impresoras

Un solo agente maneja todas las impresoras del club. Ejemplo:
- Térmica USB en mostrador → formato recibo (precios, total, método de pago)
- Térmica LAN en cocina → formato cocina (letra grande, solo items, sin precios)

El routing por categoría de producto es automático (configurado en CanchaYa).

## Auto-arranque

Después del primer pareo, el agente instala un servicio del sistema:
- **Mac**: LaunchAgent (`~/Library/LaunchAgents/ar.canchaya.fiscal-agent.plist`)
- **Windows**: acceso directo en Startup

Se inicia automáticamente al prender la PC.

## Build local

```bash
# Mac/Linux (con CUPS para USB)
go build -o canchaya-print .

# Windows (con Print Spooler para USB)
GOOS=windows GOARCH=amd64 go build -o canchaya-print.exe .

# Mac .app firmada y notarizada (requiere Apple Developer ID)
./scripts/build-mac-app.sh 0.5.0
```

Requiere Go 1.21+. No requiere libusb (se eliminó en v0.3.5).
