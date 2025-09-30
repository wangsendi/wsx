wsx-lite：WebSocket 二次封装（LITE）需求规格说明书

目标：基于 github.com/gorilla/websocket 提供一层 最小必要 封装，避免造轮子。适用于边缘盒子 ↔ 服务端的高并发、弱网环境；强调“可用、简单、可维护”。

⸻

1. 范围（Scope）
	•	提供：
	•	连接抽象 Connection（只封装 gorilla 原生 I/O，读写分离、串行写队列）。
	•	简洁路由：type → handler（JSON Envelope）。
	•	服务端 Hub：连接注册、SendTo、Broadcast。
	•	客户端：自动重连（指数退避 + 抖动）。
	•	无外部日志依赖；不强制指标/追踪。
	•	不提供：
	•	会话恢复（last_seq/补发/快照）。
	•	QoS1/ACK/重试。
	•	内置心跳（Ping/Pong）；由业务层自行实现。
	•	复杂的房间/话题、权限、鉴权、压缩、持久化。

⸻

2. 设计原则
	•	尽量使用 gorilla 原生 API：ReadMessage/WriteMessage、Upgrader。
	•	简单优先：移除非必要机制（ACK、会话恢复、内置心跳）。
	•	可插拔：路由 handler 由业务注册；日志/指标由业务注入或外挂。
	•	易运维：支持优雅关闭、错误返回，便于上层决定策略。

⸻

3. 数据模型

3.1 Envelope（JSON）

{ "ver": 1, "type": "telemetry|notice|cmd|__raw__", "ts": 1732969200, "data": {"...": "..."} }

	•	type：必填；路由键。
	•	data：业务负载（透传 RawMessage）。
	•	__raw__：解析失败的兜底类型（将整个帧作为 data 返回给 handler）。

⸻

4. 公共接口（对外 API）

4.1 服务端
	•	hub := NewHub()
	•	http.HandleFunc("/ws", hub.WSHandler(buildID func(*http.Request) string, register func(*Connection)))
	•	hub.SendTo(id string, env Envelope) error
	•	hub.Broadcast(env Envelope)

4.2 连接（服务端/客户端通用）
	•	NewConnection(ws *websocket.Conn) *Connection
	•	(*Connection).Start(ctx context.Context)  // 启动读写循环
	•	(*Connection).RegisterHandler(type string, h MessageHandler)
	•	(*Connection).SendBytes(b []byte) error   // 发送原始 JSON（上层可配合 Envelope）
	•	(*Connection).Close()

4.3 客户端
	•	cli := NewClient(url string)
	•	cli.Connect(ctx, register func(*Connection)) error // 自动重连、内部 Start
	•	cli.Send(env Envelope) error
	•	cli.Close()

⸻

5. 行为规范

5.1 读写模型
	•	读：内部 gorilla ReadMessage() 完整读取一帧；尝试 JSON 解析为 Envelope，失败则投递 __raw__。
	•	写：WriteMessage()；单 goroutine 串行，保证 socket 写安全。
	•	路由：按 env.Type 查找已注册 handler；未注册则忽略。

5.2 连接生命周期
	•	Connection.Start() 启动两个循环：readLoop + writeLoop（从 sendCh 读取并写帧）。
	•	Close()：发送 Close 帧（1000）并关闭底层连接；多次调用幂等。
	•	自动重连（客户端）：指数退避（初始 500ms、放大 1.8x、上限 30s）+ 抖动；成功连接后重置退避。

5.3 背压
	•	sendCh 缓冲（默认 1024）。满时 SendBytes 会阻塞当前调用者直至有空位或连接关闭。
	•	不做批量聚合；不做优先级。

5.4 心跳（由业务实现）
	•	包内不做 Ping/Pong。
	•	业务层可：
	•	定时发送 Envelope{type:"health"}；
	•	或调用 gorilla：WriteControl(PingMessage, ...) + SetPongHandler(...)。

⸻

6. 错误语义
	•	SendTo：若连接不存在返回 http.ErrNoLocation。
	•	SendBytes：连接已关闭时返回 "closed" 错误。
	•	Connect：若 reconnect=false 且拨号失败，立即返回错误；否则进入退避重试。

⸻

7. 配置项（默认值）
	•	写超时 writeWait = 10s。
	•	最大消息大小 maxMsgSize = 2MB（可调常量）。
	•	sendCh 缓冲大小 1024（可视内存与业务调优）。
	•	自动重连：initial=500ms，maxBackoff=30s，乘数 1.8，抖动 ±50%。

⸻

8. 性能与容量目标
	•	单连接：常规小报文（≤2KB）P95 写入 < 2ms（本机基准）。
	•	单进程：1k~5k 并发连接可稳定（依主机与 GC 调优）。
	•	内存：每连接常驻开销（不含业务数据）≤ ~32KB。

实际容量与 handler 耗时/负载相关，以上为参考基线。

⸻

9. 安全与合规
	•	DefaultUpgrader.CheckOrigin 需在业务接入层替换为白名单策略（默认放开仅用于开发环境）。
	•	鉴权（JWT/mTLS）、权限、限流在业务侧实现（本包不内置）。

⸻

10. 测试用例（验收标准）
	1.	建连/收发：客户端连接后，服务端收到 telemetry，handler 被调用。
	2.	广播/单发：Broadcast/SendTo 收到且仅对应连接收到。
	3.	并发写安全：多个 goroutine 同时调用 SendBytes，无并发写 panic，消息顺序保持为发起顺序。
	4.	关闭幂等：重复 Close() 无异常；SendBytes 在关闭后返回 "closed"。
	5.	自动重连：断开网络/杀服务端进程再恢复，客户端能重连成功并继续收发。
	6.	大消息限制：超过 maxMsgSize 的消息被拒读（或上层拒发）；不会 OOM。

⸻

11. 迭代与扩展（非本期）
	•	可靠层（ACK/重试）；会话恢复（last_seq/snapshot/patch）。
	•	心跳策略模板（封装 Ping/Pong 辅助函数）。
	•	主题/房间订阅；指标/追踪 Hook；压缩（permessage-deflate）。

⸻

12. 交付物
	•	wsx/ 源码目录（5 个文件）：envelope.go、codec.go、connection.go、server.go、client.go。
	•	example/ 最小可运行示例（服务端 + 客户端）。
	•	README：快速上手、API 列表、本规格说明书链接。

⸻

13. 时间与验收
	•	开发：1–2 天（含示例与 README）。
	•	联调：0.5 天（和现网网关/反代）。
	•	验收：通过第 10 节全部用例。