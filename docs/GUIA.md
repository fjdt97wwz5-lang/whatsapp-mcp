WhatsApp MCP 2.0.5 - Guía rápida
================================

IMPORTANTE después de actualizar a la 2.0.5:
WhatsApp MCP ahora entra a tu Tailscale como un equipo aparte, con su
propia dirección en el puerto normal de internet (443). Sigue la sección
"7. Conectar Claude en la nube" paso a paso: primero creas una clave de
Tailscale en la página de administración (paso A), luego la pegas en el
panel (paso B) y al final cambias la dirección en claude.ai (paso C).
No hace falta tocar nada del Tailscale del servidor ni de etiquetas.

Qué es
------
Un conector que deja a Claude leer tus chats de WhatsApp y enviar mensajes,
pero solo a los contactos y grupos que tú apruebes. Corre en el servidor
Windows como un servicio que arranca solo. Los mensajes se guardan en una
base de datos PostgreSQL privada, solo para WhatsApp. Se entra desde la Mac
por Tailscale con una clave y, si lo enciendes, desde Claude en la nube con
otra clave distinta, por una dirección propia de Tailscale.

Todo se maneja desde el panel web: menú Inicio > WhatsApp MCP > Panel de
WhatsApp MCP. Se abre en el navegador del servidor sin pedir clave (Windows
pide permiso de administrador).

Usa un número secundario si puedes: WhatsApp no permite oficialmente este
tipo de conexiones y podría bloquear el número.

Novedades de la 2.0.5
---------------------
- Dirección propia para Claude en la nube. WhatsApp MCP aparece en tu
  Tailscale como un equipo nuevo llamado "whatsapp-mcp" y se publica en
  https://whatsapp-mcp.TU-TAILNET.ts.net (puerto 443, sin número al final).
  La app de etiquetas y LeadsApp siguen igual: no se tocan.
- La clave de Tailscale se pega en el panel, se usa una sola vez para entrar
  y no se guarda.
- Al conectar con una clave nueva, la clave de la nube se cambia sola, y
  cuando la dirección nueva queda publicada se quita solo el Funnel viejo
  del puerto 10000.
- El método anterior (puerto 10000) sigue en el panel, plegado, por si lo
  necesitas.

Novedades de la 2.0.4
---------------------
- Claude en la nube ya puede conectarse. Antes el programa rechazaba todo
  lo que llegaba por la dirección pública (Funnel) y claude.ai decía que no
  podía llegar a la dirección.

Novedades de la 2.0.3
---------------------
- "Panel de WhatsApp MCP" abre el panel en tu navegador. Antes, en algunos
  equipos solo abría el Explorador de archivos.
- Si el navegador no se abre en unos segundos, sale una ventanita con la
  dirección y un botón "Copiar": pégala en Chrome o Edge en el servidor.
  La ventanita se cierra sola cuando el panel abre.
- El enlace vale 10 minutos y una sola vez, y solo se gasta cuando la página
  del panel carga de verdad.

Novedades de la 2.0.2
---------------------
- Al actualizar, el instalador apaga el servicio y espera a que se detenga
  de verdad, cierra las ventanas de WhatsApp MCP que sigan abiertas (Panel,
  Vincular) y reintenta copiar el programa. Ya no sale el error "Error
  abriendo archivo para escritura". Al terminar vuelve a arrancar el servicio.
- Todo lo demás es igual que en la 2.0.1.

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
1. Copia WhatsAppMCP-Setup-2.0.5.exe al servidor y ábrelo.
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

El enlace del menú Inicio vale una sola vez y por 10 minutos. Si el
navegador no se abre, sale una ventanita con la dirección y el botón
"Copiar". Si ves "El enlace ya se usó", vuelve a abrirlo desde el menú Inicio.


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


7. Conectar Claude en la nube (claude.ai)
-----------------------------------------
Sirve para que Claude use tu WhatsApp desde claude.ai (también desde el
teléfono) y para seguimientos automáticos. Usa una clave distinta a la de
la Mac. El panel y los ajustes "Solo a mano" nunca quedan en internet: por
la nube solo se llega a las herramientas de Claude.

WhatsApp MCP entra a tu Tailscale como un equipo aparte ("whatsapp-mcp") y
se publica con Funnel en el puerto 443 de ese equipo. El Tailscale del
servidor, la app de etiquetas y LeadsApp no se tocan.

Paso A. Revisar Tailscale y crear la clave (en el navegador, una sola vez)
   Entra a https://login.tailscale.com/admin con tu cuenta de Tailscale
   (la misma del servidor). Necesitas ser dueño o administrador.
   1. HTTPS encendido: menú DNS. En "HTTPS Certificates" debe decir que
      está activado. Si no, pulsa "Enable HTTPS" y acepta. (Seguro ya está,
      porque etiquetas usa Funnel.)
   2. Permiso de Funnel: menú Access controls. Busca en el texto un bloque
      "nodeAttrs" que tenga "autogroup:member" y "funnel", así:
         "nodeAttrs": [
           { "target": ["autogroup:member"], "attr": ["funnel"] }
         ]
      Si está, no cambies nada. Si no está, pulsa "Add Funnel to policy"
      (o pega ese bloque) y guarda con "Save".
      Ojo: si el bloque dice otra cosa en "target" (por ejemplo solo el
      nombre del servidor de etiquetas), añade "autogroup:member" a la
      lista o el equipo nuevo no podrá usar Funnel.
   3. Crear la clave: menú Settings > Keys > "Generate auth key...".
      - Description: whatsapp-mcp
      - Reusable: apagado (se usa una sola vez)
      - Expiration: 1 día basta (solo sirve para entrar la primera vez)
      - Ephemeral: apagado (si lo enciendes, el equipo se borra solo)
      - Pre-approved: enciéndelo si te aparece (solo sale si tu Tailscale
        pide aprobar equipos nuevos)
      - Tags: apagado
      Pulsa "Generate key" y copia la clave (empieza por tskey-auth-).
      Tailscale la muestra una sola vez. No la mandes por chat ni correo.

Paso B. Pegar la clave en el panel (en el servidor)
   1. Panel > Ajustes: "Acceso desde la nube" debe estar encendido.
   2. Panel > Conectar Claude > "Dirección propia en Tailscale".
      Pega la clave en "Clave De Tailscale". Deja el nombre "whatsapp-mcp".
      Pulsa "Conectar" y acepta el aviso.
   3. Espera a que "Estado" diga "Publicado" (unos segundos, a veces un par
      de minutos). Si dice:
      - "Esperando aprobación": en la página de Tailscale, menú Machines,
        busca whatsapp-mcp y pulsa "Approve".
      - "Con problema" y habla de HTTPS o de Funnel: revisa los puntos 1 y 2
        del paso A. El panel reintenta solo cada 30 segundos.
      - "Falta la clave": la clave venció, ya se usó o se copió mal. Crea
        otra (paso A, punto 3) y vuelve a pegarla.
   4. Cuando diga "Publicado": en la página de Tailscale, menú Machines,
      abre el menú (...) del equipo whatsapp-mcp y elige "Disable key
      expiry". Así no se desconecta solo en unos meses.
   Al conectar pasan dos cosas solas: la clave de la nube se cambia por una
   nueva (la dirección vieja deja de servir) y se quita el Funnel viejo del
   puerto 10000 del servidor. El Funnel de etiquetas no se toca.

Paso C. Poner la dirección nueva en claude.ai
   1. Panel > Conectar Claude: pulsa "Copiar dirección". Es algo como
        https://whatsapp-mcp.TU-TAILNET.ts.net/mcp/k/CLAVE-DE-LA-NUBE
      (sin ":10000"). Trátala como una contraseña.
   2. En claude.ai: Configuración > Conectores. Borra el conector WhatsApp
      viejo y pulsa "Añadir conector personalizado". Nombre: WhatsApp.
      URL: pega la dirección. Deja vacíos los datos de OAuth.
   3. En un chat nuevo de claude.ai, activa el conector WhatsApp y prueba
      "¿quién me escribió hoy y no le he respondido?".

Para cortarlo al instante: apaga "Acceso desde la nube" en Ajustes, o pulsa
"Apagar" en "Dirección propia". Para que la dirección deje de servir:
"Cambiar la clave de la nube" (luego pega la nueva en claude.ai).
"Olvidar equipo" saca a whatsapp-mcp de tu Tailscale y borra su sesión; para
volver hace falta una clave nueva (y bórralo también en Machines).
Al reiniciar el servidor, la dirección propia vuelve a encenderse sola.

Límites de la nube: 240 llamadas por minuto; tras 20 claves equivocadas en
10 minutos se pausa 10 minutos. Lo que haga Claude desde la nube sale en el
Registro como "Claude (nube)".

Método anterior (puerto 10000): sigue en Panel > Conectar Claude, plegado
en "Método anterior". Publicaba en el puerto 10000 del Tailscale del
servidor, pero claude.ai no llegaba a esa dirección.


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
