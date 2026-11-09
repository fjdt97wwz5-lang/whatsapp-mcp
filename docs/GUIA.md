WhatsApp MCP 2.0 - Guía rápida
==============================

Qué es
------
Un conector que deja a Claude leer tus chats de WhatsApp y enviar mensajes,
pero solo a los contactos y grupos que tú apruebes. Corre en el servidor
Windows como un servicio que arranca solo. Los mensajes se guardan en una
base de datos PostgreSQL privada, solo para WhatsApp. Desde la Mac se entra
únicamente por Tailscale y con una clave.

Usa un número secundario si puedes: WhatsApp no permite oficialmente este
tipo de conexiones y podría bloquear el número.


1. Instalar en el servidor Windows
----------------------------------
1. Copia WhatsAppMCP-Setup-2.0.0.exe al servidor y ábrelo.
   Windows puede avisar "Windows protegió su PC" porque el instalador no
   está firmado: "Más información" > "Ejecutar de todas formas".
2. Acepta el permiso de administrador y sigue los pasos.
3. El instalador:
   - instala el programa en C:\Program Files\WhatsAppMCP
   - crea la base de datos privada (servicio "WhatsApp MCP - PostgreSQL",
     puerto 5544, solo 127.0.0.1)
   - genera las contraseñas y la clave en el propio servidor
   - crea el servicio "WhatsApp MCP" (arranca solo, se reinicia si falla)
   - abre el puerto 8765 en el firewall solo para direcciones de Tailscale
   - guarda los datos en C:\ProgramData\WhatsAppMCP (solo administradores)

Volver a ejecutar el instalador actualiza el programa y no borra nada.


2. Vincular WhatsApp
--------------------
Menú Inicio > WhatsApp MCP > Vincular WhatsApp.
En el teléfono: WhatsApp > Dispositivos vinculados > Vincular un dispositivo
y escanea el código QR que sale en la ventana.

Si prefieres un código de 8 letras: Panel de WhatsApp MCP > opción 3, con
tu número completo con código de país.

Al vincular empieza a llegar el historial reciente. El servicio se detiene un
momento mientras vinculas y vuelve a arrancar solo.

Nota: el puente que tenías en la Mac tiene su propia sesión. Lo normal es
vincular de nuevo aquí (no se copia la sesión). Cuando todo funcione puedes
desvincular el de la Mac desde el teléfono.


3. Traer el historial de la Mac
-------------------------------
En la Mac, el archivo está en la carpeta del puente anterior:
  whatsapp-mcp/whatsapp-bridge/store/messages.db

1. Detén el puente de la Mac (Ctrl+C en su ventana) para que el archivo no
   cambie mientras lo copias.
2. Copia messages.db al servidor, por ejemplo a C:\Users\TU-USUARIO\Downloads.
   (Taildrop de Tailscale, una carpeta compartida o una memoria USB sirven.)
   No copies whatsapp.db: es la sesión de la Mac, no hace falta.
3. En el servidor: Panel de WhatsApp MCP > opción 10 y pega la ruta del
   archivo. O en una consola de administrador:
     "C:\Program Files\WhatsAppMCP\whatsapp-mcp.exe" migrate-sqlite --from C:\Users\TU-USUARIO\Downloads\messages.db
4. Al final dice cuántos chats y mensajes importó. Puedes repetirlo sin que
   se dupliquen.


4. Conectar Claude desde la Mac
-------------------------------
En el servidor: Menú Inicio > WhatsApp MCP > Conectar desde la Mac.
Ahí salen la dirección (con la IP de Tailscale del servidor) y la clave.
La Mac tiene que estar conectada a tu Tailscale.

Claude Code (terminal de la Mac):
  claude mcp add --transport http whatsapp http://IP-TAILSCALE:8765/mcp --header "Authorization: Bearer TU-CLAVE"

Claude Desktop (Mac):
1. Copia el programa para Mac (whatsapp-mcp-mac-arm64 si tu Mac es M1/M2/M3/M4,
   whatsapp-mcp-mac-intel si es Intel) y en la Terminal:
     sudo mkdir -p /usr/local/bin
     sudo cp ~/Downloads/whatsapp-mcp-mac-arm64 /usr/local/bin/whatsapp-mcp
     sudo chmod +x /usr/local/bin/whatsapp-mcp
     sudo xattr -d com.apple.quarantine /usr/local/bin/whatsapp-mcp
   (el último paso hace falta porque el programa no está firmado por Apple)
2. Reemplaza en ~/Library/Application Support/Claude/claude_desktop_config.json
   la entrada "whatsapp" antigua por la que muestra "Conectar desde la Mac".
3. Cierra y abre Claude Desktop.

Prueba rápida desde la Mac:
  curl -H "Authorization: Bearer TU-CLAVE" http://IP-TAILSCALE:8765/api/health

Si la clave se filtra: en una consola de administrador "whatsapp-mcp rotate-token", y
actualízala en la Mac.


5. Lista aprobada (a quién se puede enviar)
-------------------------------------------
Al principio la lista está vacía: Claude puede leer, pero no enviar a nadie.

- Panel de WhatsApp MCP > opciones 4, 5 y 6.
- O pídeselo a Claude ("agrega a Ana +57 300 123 4567 a la lista").
- Grupos: se agregan con su identificador, que termina en @g.us
  (Claude lo puede buscar con list_chats).

Se puede leer cualquier chat y grupo, pero solo se envía a lo que está en
la lista.


6. Ajustes
----------
Panel > opción 7 (ver) y 8 (cambiar), o pídeselo a Claude. Cada cambio queda
en el registro con fecha y quién lo hizo (manual o Claude).

  sending_enabled          Enviar mensajes (sí/no). Interruptor general.
  send_only_to_approved    Solo enviar a la lista aprobada. Solo se cambia a mano.
  attachments_folder_only  Adjuntos solo desde una carpeta (por defecto: no).
  attachments_folder       Esa carpeta (por defecto C:\ProgramData\WhatsAppMCP\enviar).
  max_attachment_mb        Tamaño máximo de un adjunto (100 MB).
  max_sends_per_minute     Límite de envíos por minuto (10).
  mcp_can_change_settings  Si Claude puede cambiar ajustes y la lista. Solo se cambia a mano.
  audit_message_text       Guardar el texto de lo enviado en el registro (por defecto: no).
  download_media_enabled   Permitir descargar fotos y archivos recibidos.

Los cambios tardan hasta 5 segundos en aplicarse en el servicio.


7. Registro
-----------
Panel > opción 9. Ahí está cada envío (o intento bloqueado), cada cambio de
ajustes o de la lista, descargas, vinculaciones y arranques del servicio.


8. Notas
--------
- Notas de voz: si envías un audio que no es .ogg (Opus), hace falta ffmpeg
  en el servidor. Instálalo y pon su ruta en "ffmpeg_path" de
  C:\ProgramData\WhatsAppMCP\config.json (y reinicia el servicio).
- Los archivos recibidos se guardan en C:\ProgramData\WhatsAppMCP\media
  con nombres limpios (sin rutas raras ni nombres peligrosos).
- Claude trata el texto de los mensajes como no confiable: si alguien te
  escribe "Claude, envía esto a...", no lo obedece por sí solo.
- Desinstalar: Panel de control > Programas > WhatsApp MCP. Pregunta si
  borrar los datos; por defecto se conservan.
