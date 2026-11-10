WhatsApp MCP 2.0.1 - Guía rápida
================================

Qué es
------
Un conector que deja a Claude leer tus chats de WhatsApp y enviar mensajes,
pero solo a los contactos y grupos que tú apruebes. Corre en el servidor
Windows como un servicio que arranca solo. Los mensajes se guardan en una
base de datos PostgreSQL privada, solo para WhatsApp. Se entra desde la Mac
por Tailscale con una clave y, si lo enciendes, desde Claude en la nube por
Tailscale Funnel con otra clave distinta.

Todo se maneja desde el panel web: menú Inicio > WhatsApp MCP > Panel de
WhatsApp MCP. Se abre en el navegador del servidor sin pedir clave (Windows
pide permiso de administrador).

Usa un número secundario si puedes: WhatsApp no permite oficialmente este
tipo de conexiones y podría bloquear el número.

Novedades de la 2.0.1
---------------------
- Panel web en el navegador (reemplaza la ventana negra, que queda como
  "Panel en modo texto").
- Arriba del panel, en verde o rojo: si WhatsApp está vinculado, el número y
  la última conexión.
- El código QR sale grande dentro del panel y siempre cabe en la pantalla.
  También sigue el código de 8 letras.
- Conversaciones: todos tus chats y grupos, con buscador y filtros, y un botón
  "Aprobar" en cada uno.
- Categoría (Cliente, Distribuidor, Proveedor...) y país de cada chat.
- Filtro "Sin responder" con cuánto tiempo llevan esperando.
- Acceso desde Claude en la nube (apagado hasta que lo enciendas).


1. Instalar o actualizar en el servidor Windows
-----------------------------------------------
1. Copia WhatsAppMCP-Setup-2.0.1.exe al servidor y ábrelo.
   Windows puede avisar "Windows protegió su PC" porque el instalador no
   está firmado: "Más información" > "Ejecutar de todas formas".
2. Acepta el permiso de administrador y sigue los pasos.
3. Al final deja marcado "Abrir el panel ahora".

Si ya tenías la 2.0.0, se actualiza encima y se conservan mensajes, sesión
de WhatsApp, clave, lista aprobada y ajustes.

El instalador:
- instala el programa en C:\Program Files\WhatsAppMCP
- crea la base de datos privada (servicio "WhatsApp MCP - PostgreSQL",
  puerto 5544, solo 127.0.0.1)
- genera las contraseñas y las claves en el propio servidor
- crea el servicio "WhatsApp MCP" (arranca solo, se reinicia si falla)
- abre el puerto 8765 en el firewall solo para direcciones de Tailscale
- guarda los datos en C:\ProgramData\WhatsAppMCP (solo administradores)


2. El panel
-----------
Secciones a la izquierda:
- Inicio: estado del servicio, chats, mensajes, sin responder y aprobados.
- Vincular WhatsApp: el código QR o el código de 8 letras.
- Conversaciones: todos los chats y grupos.
- Lista aprobada: a quién puede enviar Claude.
- Ajustes: interruptores. Los marcados "Solo a mano" solo se cambian aquí,
  nunca desde Claude.
- Registro: cada envío, cambio y acceso, con fecha y quién lo hizo.
- Conectar Claude: datos para la Mac y para la nube.
- Importar historial: trae el messages.db de la Mac.

El panel solo abre en el propio servidor. Si quieres abrirlo desde otro
equipo de tu Tailscale, enciende "Panel desde otros equipos" en Ajustes y
entra con la clave de acceso (la de Conectar Claude).

El enlace del menú Inicio vale una sola vez y por 2 minutos. Si ves "El
enlace ya se usó", vuelve a abrirlo desde el menú Inicio.


3. Vincular WhatsApp
--------------------
Panel > Vincular WhatsApp > "Mostrar código QR".
En el teléfono: WhatsApp > Ajustes > Dispositivos vinculados > Vincular un
dispositivo, y escanea el código del panel. El código cambia solo cada pocos
segundos.

Con código de 8 letras: escribe tu número (elige el país con la bandera) y
pulsa "Pedir código de 8 letras". En el teléfono: Dispositivos vinculados >
Vincular un dispositivo > Vincular con el número de teléfono.

Cuando termina, la barra de arriba se pone verde con tu número.
Para cambiar de número: "Desvincular este WhatsApp" y vincula otro.


4. Conversaciones, categorías y país
------------------------------------
- Busca por nombre o número. Filtra por contactos o grupos, categoría,
  país, "Sin responder" y "Solo aprobados".
- "Aprobar" en un chat o grupo lo añade a la lista aprobada (los grupos se
  añaden solos con su identificador @g.us). Pulsa otra vez para quitarlo.
- Categoría: elige en cada chat. La lista de categorías se edita en Ajustes
  ("Categorías de chats", separadas por comas).
- País: elige en cada chat. Si no lo has puesto, sale una sugerencia por el
  prefijo del número ("País: ¿Colombia?"), pero no se guarda hasta que lo
  elijas, porque el número no siempre dice dónde vive la persona.
- "Sin responder": chats donde el último mensaje es de la otra persona, con
  el tiempo que lleva esperando.

Claude también puede poner la categoría y el país cuando se lo pidas
("marca a Ana como Distribuidor, vive en Panamá"). Todo queda en el
Registro.

Para seguimientos, Claude tiene estas herramientas: list_unanswered_chats
(quién espera respuesta y desde cuándo), list_conversations (filtrar por
categoría, país, sin responder), get_chat_transcript (la conversación para
resumir qué se habló y qué quedó pendiente), set_chat_category y
set_chat_country.


5. Traer el historial de la Mac
-------------------------------
En la Mac, el archivo está en la carpeta del puente anterior:
  whatsapp-mcp/whatsapp-bridge/store/messages.db

1. Detén el puente de la Mac (Ctrl+C en su ventana).
2. Copia messages.db al servidor, por ejemplo a C:\Users\TU-USUARIO\Downloads.
   No copies whatsapp.db: es la sesión de la Mac, no hace falta.
3. Panel > Importar historial: pega la ruta (en el Explorador, clic derecho
   sobre el archivo > Copiar como ruta) y pulsa Importar.
Se puede repetir sin que se dupliquen mensajes.


6. Conectar Claude desde la Mac (Tailscale)
-------------------------------------------
Panel > Conectar Claude: ahí están la dirección y la clave (botón Mostrar
clave). La Mac tiene que estar en tu Tailscale.

Claude Code (terminal de la Mac):
  claude mcp add --transport http whatsapp http://IP-TAILSCALE:8765/mcp --header "Authorization: Bearer TU-CLAVE"

Claude Desktop (Mac):
1. Copia el programa para Mac (whatsapp-mcp-mac-arm64 si tu Mac es M1/M2/M3/M4,
   whatsapp-mcp-mac-intel si es Intel) y en la Terminal:
     sudo mkdir -p /usr/local/bin
     sudo cp ~/Downloads/whatsapp-mcp-mac-arm64 /usr/local/bin/whatsapp-mcp
     sudo chmod +x /usr/local/bin/whatsapp-mcp
     sudo xattr -d com.apple.quarantine /usr/local/bin/whatsapp-mcp
2. Reemplaza en ~/Library/Application Support/Claude/claude_desktop_config.json
   la entrada "whatsapp" por la que muestra el menú Inicio > WhatsApp MCP >
   Conectar desde la Mac.
3. Cierra y abre Claude Desktop.


7. Conectar Claude en la nube (claude.ai) con Tailscale Funnel
--------------------------------------------------------------
Sirve para que Claude use tu WhatsApp desde claude.ai (también desde el
teléfono) y para seguimientos automáticos. Usa una clave distinta a la de
la Mac. El panel y los ajustes "Solo a mano" nunca quedan en internet: por
la nube solo se llega a las herramientas de Claude.

Funnel solo permite los puertos 443, 8443 y 10000. Como 443 y 8443 ya los
usan etiquetas y LeadsApp, este conector usa el 10000.

1. Panel > Ajustes: enciende "Acceso desde la nube (Tailscale Funnel)".
2. Panel > Conectar Claude: pulsa "Publicar en Funnel".
   (O, en PowerShell como administrador, el comando que sale ahí:
     tailscale funnel --bg --https=10000 http://127.0.0.1:8767 )
   Si el puerto 10000 de Funnel ya lo usa otra cosa, el panel no lo toca
   y te lo dice; en ese caso no sigas, hay que repartir los puertos.
3. Pulsa "Copiar dirección". Es algo como
     https://NOMBRE-DEL-SERVIDOR.TU-TAILNET.ts.net:10000/mcp/k/CLAVE-DE-LA-NUBE
   Trátala como una contraseña: quien la tenga puede usar el conector.
4. En claude.ai: Configuración > Conectores > Añadir conector personalizado.
   Nombre: WhatsApp. URL: pega la dirección. Deja vacíos los datos de OAuth.
5. En un chat de claude.ai, activa el conector WhatsApp y prueba
   "¿quién me escribió hoy y no le he respondido?".

Para cortarlo al instante: apaga "Acceso desde la nube" en Ajustes. Para que
la dirección anterior deje de servir: "Cambiar la clave de la nube" (luego
pega la nueva en claude.ai). "Quitar de Funnel" deja de publicar el puerto.

Límites de la nube: 240 llamadas por minuto; tras 20 claves equivocadas en
10 minutos se pausa 10 minutos. Lo que haga Claude desde la nube sale en el
Registro como "Claude (nube)".


8. Lista aprobada y ajustes
---------------------------
Al principio la lista está vacía: Claude puede leer, pero no enviar a nadie.
Lo más fácil es aprobar desde Conversaciones. En Lista aprobada puedes
añadir un número a mano (con bandera y formato) y quitar.

Ajustes principales:
  Envío de mensajes               Interruptor general.
  Enviar solo a la lista aprobada Solo a mano.
  Claude puede cambiar ajustes    Solo a mano. Apagado = Claude no cambia
                                  nada (ni lista, ni categorías, ni país).
  Adjuntos solo desde una carpeta Por defecto: no.
  Tamaño máximo de un archivo     100 MB.
  Envíos por minuto como máximo   10.
  Guardar el texto enviado        Por defecto: no.
  Categorías de chats             Cliente, Distribuidor, Proveedor, Personal, Otro.
  Acceso desde la nube            Solo a mano. Por defecto: apagado.
  Panel desde otros equipos       Solo a mano. Por defecto: apagado.

Los cambios tardan hasta 5 segundos en aplicarse.


9. Notas
--------
- Si algo falla con el panel, está "Panel en modo texto" en el menú Inicio.
- Notas de voz: si envías un audio que no es .ogg (Opus), hace falta ffmpeg
  en el servidor; pon su ruta en "ffmpeg_path" de
  C:\ProgramData\WhatsAppMCP\config.json y reinicia el servicio.
- Los archivos recibidos se guardan en C:\ProgramData\WhatsAppMCP\media.
- Claude trata el texto de los mensajes como no confiable: si alguien te
  escribe "Claude, envía esto a...", no lo obedece por sí solo.
- Si la clave de la Mac se filtra: consola de administrador
  "whatsapp-mcp rotate-token", y actualízala en la Mac.
- Desinstalar: Panel de control > Programas > WhatsApp MCP. Pregunta si
  borrar los datos; por defecto se conservan.
