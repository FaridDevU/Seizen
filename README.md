# Seizen

Aplicación de escritorio para organizar proyectos. El ejecutable usa Wails y
Go; React se renderiza dentro del WebView nativo y no se publica como sitio web.

## Desarrollo

Requisitos: Go 1.25+, Node.js y Wails 2.13.

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
wails dev
```

Seizen crea automáticamente una base SQLite local en
`%APPDATA%\Seizen\seizen.db`. Los proyectos nuevos y los repositorios clonados
se guardan por defecto en `~/Seizen/Projects`; la ubicación puede cambiarse
desde el diálogo **Nuevo proyecto**.

Para generar el ejecutable de Windows:

```powershell
wails build -clean
```

El resultado queda en `build/bin/Seizen.exe`. La plantilla Incus para los
workspaces de Coder está documentada en [`infra/README.md`](infra/README.md).
