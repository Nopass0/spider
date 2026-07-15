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
)
