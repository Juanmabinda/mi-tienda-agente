# Mi Tienda Print

Agente local que conecta Mi Tienda con impresoras del comercio:
- **Térmicas LAN** (ESC/POS): tickets, comandas, recibos
- **Térmicas USB** (ESC/POS): impresoras conectadas por cable
- **Fiscales LAN**: Hasar 2G, Epson TM-T900FA

Se distribuye como un ejecutable único — no requiere instalar nada extra.

## Descargar

Descargá desde [Releases](https://github.com/Juanmabinda/mi-tienda-agente/releases/latest):

| Plataforma | Archivo | USB | LAN |
|---|---|---|---|
| Mac | `mi-tienda-print-mac.zip` (.app firmada y notarizada) | ✅ via CUPS | ✅ |
| Windows | `mi-tienda-print.exe` | ✅ via Print Spooler | ✅ |

## Instalar

1. **Descargar** el agente desde el link de arriba
2. **Abrir** el ejecutable (Mac: descomprimir y doble click en la .app)
3. **Parear**: el agente muestra un código de 6 caracteres en pantalla
4. En Mi Tienda → **Impresoras** → pegar el código en "Conectar agente"
5. Listo — el agente guarda el token y se configura para arrancar solo con la PC

No hace falta copiar tokens ni archivos. El código se ingresa una sola vez.

## Detección automática

Al conectarse, el agente escanea:
- **USB**: impresoras instaladas en el sistema (CUPS en Mac, Print Spooler en Windows)
- **LAN**: red local en puertos 9100 (ESC/POS), 7000 (Hasar), 8000 (Epson)

Las impresoras detectadas aparecen automáticamente en Mi Tienda con un botón "Usar" para darlas de alta.

## Múltiples impresoras

Un solo agente maneja todas las impresoras del comercio. Ejemplo:
- Térmica USB en mostrador → formato recibo (precios, total, método de pago)
- Térmica LAN en cocina → formato cocina (letra grande, solo items, sin precios)

El routing por categoría de producto es automático (configurado en Mi Tienda).

## Auto-arranque

Después del primer pareo, el agente instala un servicio del sistema:
- **Mac**: LaunchAgent (`~/Library/LaunchAgents/app.mitienda.print-agent.plist`)
- **Windows**: acceso directo en Startup

Se inicia automáticamente al prender la PC.

## Build local

```bash
# Mac/Linux (con CUPS para USB)
go build -o mi-tienda-print .

# Windows (con Print Spooler para USB)
GOOS=windows GOARCH=amd64 go build -o mi-tienda-print.exe .

# Mac .app firmada y notarizada (requiere Apple Developer ID)
./scripts/build-mac-app.sh 1.0.0
```

Requiere Go 1.21+.
