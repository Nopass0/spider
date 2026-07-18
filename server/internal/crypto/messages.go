package crypto

// Типы сообщений, переносимых в Envelope. Общий набор для сервера и клиента.
const (
	// MsgEnrollRequest — агент → сервер: запрос регистрации по токену.
	MsgEnrollRequest = "enroll.request"
	// MsgEnrollResponse — сервер → агент: device_id + подтверждение.
	MsgEnrollResponse = "enroll.response"

	// MsgCommand — сервер → агент: команда на выполнение.
	MsgCommand = "command"
	// MsgCommandAck — агент → сервер: принято в работу (с id задачи).
	MsgCommandAck = "command.ack"
	// MsgCommandResult — агент → сервер: результат выполнения.
	MsgCommandResult = "command.result"

	// MsgHeartbeat — агент → сервер: подтверждение жизни + сист.инфо.
	MsgHeartbeat = "heartbeat"
	// MsgServerInfo — сервер → агент: глобальные настройки (toggle и т.д.).
	MsgServerInfo = "server.info"

	// MsgPing / MsgPong — транспортные keepalive.
	MsgPing = "ping"
	MsgPong = "pong"

	// --- Streaming-терминал (PTY) ---
	// MsgTerminalOpen — admin → agent: создать PTY-сессию.
	MsgTerminalOpen = "terminal.open"
	// MsgTerminalInput — admin → agent: байты ввода в PTY.
	MsgTerminalInput = "terminal.input"
	// MsgTerminalResize — admin → agent: изменить размер PTY.
	MsgTerminalResize = "terminal.resize"
	// MsgTerminalClose — admin → agent: закрыть PTY.
	MsgTerminalClose = "terminal.close"
	// MsgTerminalOutput — agent → admin: поток вывода PTY.
	MsgTerminalOutput = "terminal.output"
	// MsgTerminalExit — agent → admin: PTY завершён с exit-кодом.
	MsgTerminalExit = "terminal.exit"

	// --- Трансляция экрана (MJPEG) ---
	// MsgScreenStart — admin → agent: начать захват кадров.
	MsgScreenStart = "screen.start"
	// MsgScreenStop — admin → agent: остановить захват.
	MsgScreenStop = "screen.stop"
	// MsgScreenFrame — agent → admin: JPEG-кадр потока.
	MsgScreenFrame = "screen.frame"

	// --- Одиночные скриншоты ---
	// MsgScreenshotSnap — admin → agent: сделать один кадр.
	MsgScreenshotSnap = "screenshot.snap"
	// MsgScreenshotDone — agent → admin: кадр готов (сохраняется на сервере).
	MsgScreenshotDone = "screenshot.done"
)
