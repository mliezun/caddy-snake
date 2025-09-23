package caddysnake

// #include "caddysnake.h"
import "C"
import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ASGI: Implementation

// Asgi stores a reference to a Python Asgi application
type Asgi struct {
	app         *C.AsgiApp
	asgiPattern string
	logger      *zap.Logger
}

var asgiAppCache map[string]*Asgi = map[string]*Asgi{}

// NewAsgi imports a Python ASGI app
func NewAsgi(asgiPattern, workingDir, venvPath string, lifespan bool, logger *zap.Logger) (*Asgi, error) {
	shard := asgiState.shardFor(0)
	shard.Lock()
	defer shard.Unlock()

	if app, ok := asgiAppCache[asgiPattern]; ok {
		return app, nil
	}

	moduleApp := strings.Split(asgiPattern, ":")
	if len(moduleApp) != 2 {
		return nil, errors.New("expected pattern $(MODULE_NAME):$(VARIABLE_NAME)")
	}
	moduleName := C.CString(moduleApp[0])
	defer C.free(unsafe.Pointer(moduleName))
	appName := C.CString(moduleApp[1])
	defer C.free(unsafe.Pointer(appName))

	var packagesPath *C.char = nil
	if venvPath != "" {
		sitePackagesPath, err := findSitePackagesInVenv(venvPath)
		if err != nil {
			return nil, err
		}
		packagesPath = C.CString(sitePackagesPath)
		defer C.free(unsafe.Pointer(packagesPath))
	}

	var workingDirPath *C.char = nil
	if workingDir != "" {
		workingDirAbs, err := findWorkingDirectory(workingDir)
		if err != nil {
			return nil, err
		}
		workingDirPath = C.CString(workingDirAbs)
		defer C.free(unsafe.Pointer(workingDirPath))
	}

	var app *C.AsgiApp
	pythonMainThread.do(func() {
		app = C.AsgiApp_import(moduleName, appName, workingDirPath, packagesPath)
	})
	if app == nil {
		return nil, errors.New("failed to import module")
	}

	var err error

	if lifespan {
		var status C.uint8_t
		pythonMainThread.do(func() {
			status = C.AsgiApp_lifespan_startup(app)
		})
		if uint8(status) == 0 {
			err = errors.New("startup failed")
		}
	}

	result := &Asgi{app, asgiPattern, logger}
	asgiAppCache[asgiPattern] = result
	return result, err
}

// Cleanup deallocates CGO resources used by Asgi app
func (m *Asgi) Cleanup() (err error) {
	if m != nil && m.app != nil {
		shard := asgiState.shardFor(0)
		shard.Lock()
		if _, ok := asgiAppCache[m.asgiPattern]; !ok {
			shard.Unlock()
			return
		}
		delete(asgiAppCache, m.asgiPattern)
		shard.Unlock()

		var status C.uint8_t
		pythonMainThread.do(func() {
			status = C.AsgiApp_lifespan_shutdown(m.app)
			if uint8(status) == 0 {
				err = errors.New("shutdown failure")
			}
			C.AsgiApp_cleanup(m.app)
		})
	}
	return
}

type WebsocketState uint8

const (
	WS_STARTING WebsocketState = iota + 2
	WS_CONNECTED
	WS_DISCONNECTED
)

// AsgiRequestHandler stores pointers to the request and the response writer
type AsgiRequestHandler struct {
	event                   *C.AsgiEvent
	w                       http.ResponseWriter
	r                       *http.Request
	completedBody           bool
	completedResponse       bool
	accumulatedResponseSize int
	done                    chan error

	operations chan AsgiOperations

	websocket      bool
	websocketState WebsocketState
	websocketConn  *websocket.Conn
}

func (h *AsgiRequestHandler) Cleanup() {
	h.completedResponse = true
	h.operations <- AsgiOperations{stop: true}
}

// AsgiOperations stores operations that should be executed in the background
type AsgiOperations struct {
	stop bool
	op   func()
}

func (h *AsgiRequestHandler) consume() {
	for {
		o := <-h.operations
		if o.op != nil {
			o.op()
		}
		if o.stop {
			if h.event != nil {
				pythonMainThread.do(func() {
					C.AsgiEvent_cleanup(h.event)
				})
			}
			close(h.operations)
			break
		}
	}
}

// NewAsgiRequestHandler initializes handler and starts queue that consumes operations
// in the background.
func NewAsgiRequestHandler(w http.ResponseWriter, r *http.Request, websocket bool) *AsgiRequestHandler {
	h := &AsgiRequestHandler{
		w:    w,
		r:    r,
		done: make(chan error, 2),

		operations: make(chan AsgiOperations, 16),

		websocket: websocket,
	}
	go h.consume()
	return h
}

const asgiShardCount = 4

type asgiShard struct {
	sync.RWMutex
	handlers map[uint64]*AsgiRequestHandler
}

type AsgiGlobalState struct {
	requestCounter uint64 // atomic
	shards         [asgiShardCount]*asgiShard
}

func newAsgiGlobalState() *AsgiGlobalState {
	ags := &AsgiGlobalState{}
	for i := 0; i < asgiShardCount; i++ {
		ags.shards[i] = &asgiShard{
			handlers: make(map[uint64]*AsgiRequestHandler),
		}
	}
	return ags
}

func (s *AsgiGlobalState) shardFor(id uint64) *asgiShard {
	return s.shards[id%asgiShardCount]
}

func (s *AsgiGlobalState) Request(h *AsgiRequestHandler) uint64 {
	id := atomic.AddUint64(&s.requestCounter, 1)
	shard := s.shardFor(id)
	shard.Lock()
	shard.handlers[id] = h
	shard.Unlock()
	return id
}

func (s *AsgiGlobalState) Cleanup(requestID uint64) {
	shard := s.shardFor(requestID)
	shard.Lock()
	delete(shard.handlers, requestID)
	shard.Unlock()
}

func (s *AsgiGlobalState) GetHandler(requestID uint64) *AsgiRequestHandler {
	shard := s.shardFor(requestID)
	shard.RLock()
	h := shard.handlers[requestID]
	shard.RUnlock()
	return h
}

func initAsgi() {
	asgiStateOnce.Do(func() {
		asgiState = newAsgiGlobalState()
	})
}

var (
	asgiState     *AsgiGlobalState
	asgiStateOnce sync.Once
)

func getRemoteHostPort(r *http.Request) (string, int) {
	host, port, _ := net.SplitHostPort(r.RemoteAddr)
	portN, _ := strconv.Atoi(port)
	return host, portN
}

func needsWebsocketUpgrade(r *http.Request) bool {
	if r.Method != "GET" {
		return false
	}

	containsConnectionUpgrade := false
	for _, v := range r.Header.Values("connection") {
		if strings.Contains(strings.ToLower(v), "upgrade") {
			containsConnectionUpgrade = true
			break
		}
	}
	if !containsConnectionUpgrade {
		return false
	}

	containsUpgradeWebsockets := false
	for _, v := range r.Header.Values("upgrade") {
		if strings.Contains(strings.ToLower(v), "websocket") {
			containsUpgradeWebsockets = true
			break
		}
	}

	return containsUpgradeWebsockets
}

func buildAsgiHeaders(r *http.Request, websocket bool) (*MapKeyVal, *MapKeyVal, error) {
	decodedPath, err := url.PathUnescape(r.URL.Path)
	if err != nil {
		return nil, nil, err
	}
	var connType, scheme string
	if websocket {
		connType = "websocket"
		scheme = "ws"
		if r.TLS != nil {
			scheme = "wss"
		}
	} else {
		connType = "http"
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	scopeMap := map[string]string{
		"type":         connType,
		"http_version": fmt.Sprintf("%d.%d", r.ProtoMajor, r.ProtoMinor),
		"method":       r.Method,
		"scheme":       scheme,
		"path":         decodedPath,
		"raw_path":     r.URL.EscapedPath(),
		"query_string": r.URL.RawQuery,
		"root_path":    "",
	}
	scope := NewMapKeyVal(len(scopeMap))
	for k, v := range scopeMap {
		scope.Append(k, v)
	}

	requestHeaders := NewMapKeyVal(len(r.Header))
	for k, items := range r.Header {
		if k == "Proxy" {
			// golang cgi issue 16405
			continue
		}

		joinStr := ", "
		if k == "Cookie" {
			joinStr = "; "
		}

		requestHeaders.Append(strings.ToLower(k), strings.Join(items, joinStr))
	}

	return requestHeaders, scope, nil
}

// HandleRequest passes request down to Python ASGI app and writes responses and headers.
func (m *Asgi) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	host, port := getHostPort(r)
	serverHostStr := C.CString(host)
	defer C.free(unsafe.Pointer(serverHostStr))

	clientHost, clientPort := getRemoteHostPort(r)
	clientHostStr := C.CString(clientHost)
	defer C.free(unsafe.Pointer(clientHostStr))

	websocket := needsWebsocketUpgrade(r)

	requestHeaders, scope, err := buildAsgiHeaders(r, websocket)
	if err != nil {
		return err
	}
	defer requestHeaders.Cleanup()
	defer scope.Cleanup()

	arh := NewAsgiRequestHandler(w, r, websocket)
	defer arh.Cleanup()

	requestID := asgiState.Request(arh)
	defer asgiState.Cleanup(requestID)

	var subprotocols *C.char = nil
	if websocket {
		subprotocols = C.CString(r.Header.Get("sec-websocket-protocol"))
		defer C.free(unsafe.Pointer(subprotocols))
	}

	pythonMainThread.do(func() {
		C.AsgiApp_handle_request(
			m.app,
			C.uint64_t(requestID),
			scope.m,
			requestHeaders.m,
			clientHostStr,
			C.int(clientPort),
			serverHostStr,
			C.int(port),
			subprotocols,
		)
	})

	if err := <-arh.done; err != nil {
		w.WriteHeader(500)
		m.logger.Debug(err.Error())
	}

	return nil
}

func (h *AsgiRequestHandler) SetWebsocketError(event *C.AsgiEvent, err error) {
	closeError, isClose := err.(*websocket.CloseError)
	closeCode := 1005
	if isClose {
		closeCode = closeError.Code
	}
	closeStr := fmt.Sprintf("%d", closeCode)
	bodyStr := C.CString(closeStr)
	bodyLen := C.size_t(len(closeStr))
	defer C.free(unsafe.Pointer(bodyStr))
	h.websocketState = WS_DISCONNECTED
	h.websocketConn.Close()

	pythonMainThread.do(func() {
		C.AsgiEvent_websocket_set_disconnected(event)
		C.AsgiEvent_set_websocket(event, bodyStr, bodyLen, C.uint8_t(0), C.uint8_t(0))
	})

	h.done <- fmt.Errorf("websocket closed: %d", closeCode)
}

func (h *AsgiRequestHandler) ReadWebsocketMessage(event *C.AsgiEvent) {
	mt, message, err := h.websocketConn.ReadMessage()
	if err != nil {
		h.SetWebsocketError(event, err)
		return
	}
	message = append(message, 0)
	bodyStr := (*C.char)(unsafe.Pointer(&message[0]))
	bodyLen := C.size_t(len(message) - 1)

	messageType := C.uint8_t(0)
	if mt == websocket.BinaryMessage {
		messageType = C.uint8_t(1)
	}

	pythonMainThread.do(func() {
		C.AsgiEvent_set_websocket(event, bodyStr, bodyLen, messageType, C.uint8_t(0))
	})
}

func (h *AsgiRequestHandler) DisconnectWebsocket(event *C.AsgiEvent) {
	pythonMainThread.do(func() {
		C.AsgiEvent_websocket_set_disconnected(event)
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(0))
	})
	h.done <- errors.New("websocket closed - receive start")
}

func (h *AsgiRequestHandler) ConnectWebsocket(event *C.AsgiEvent) {
	h.websocketState = WS_STARTING
	C.AsgiEvent_websocket_set_connected(event)
	C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(0))
}

func (h *AsgiRequestHandler) HandleWebsocket(event *C.AsgiEvent) C.uint8_t {
	switch h.websocketState {
	case WS_STARTING:
		panic("ASSERTION: websocket state should be WS_CONNECTED or WS_DISCONNECTED")
	case WS_CONNECTED:
		go h.ReadWebsocketMessage(event)
	case WS_DISCONNECTED:
		go h.DisconnectWebsocket(event)
	default:
		h.ConnectWebsocket(event)
	}
	return C.uint8_t(1)
}

func (h *AsgiRequestHandler) SetEvent(event *C.AsgiEvent) {
	h.event = event
}

func (h *AsgiRequestHandler) readBody(event *C.AsgiEvent) {
	var bodyStr *C.char
	var bodyLen C.size_t
	var moreBody C.uint8_t
	if !h.completedBody {
		buffer := make([]byte, 1<<16)
		n, err := h.r.Body.Read(buffer)
		if err != nil && err != io.EOF {
			h.done <- err
			return
		}
		h.completedBody = (err == io.EOF)
		buffer = append(buffer[:n], 0)
		bodyStr = (*C.char)(unsafe.Pointer(&buffer[0]))
		bodyLen = C.size_t(len(buffer) - 1) // -1 to remove null-terminator
	}

	if h.completedBody {
		moreBody = C.uint8_t(0)
	} else {
		moreBody = C.uint8_t(1)
	}

	pythonMainThread.do(func() {
		C.AsgiEvent_set(event, bodyStr, bodyLen, moreBody, C.uint8_t(0))
	})
}

func (h *AsgiRequestHandler) ReceiveStart(event *C.AsgiEvent) C.uint8_t {
	h.operations <- AsgiOperations{op: func() {
		h.readBody(event)
	}}
	return C.uint8_t(1)
}

func (h *AsgiRequestHandler) UpgradeWebsockets(headers http.Header, event *C.AsgiEvent) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout:  time.Minute,
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	wsConn, err := upgrader.Upgrade(h.w, h.r, headers)
	if err != nil {
		h.websocketState = WS_DISCONNECTED
		C.AsgiEvent_websocket_set_disconnected(event)
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		return
	}
	h.websocketState = WS_CONNECTED
	h.websocketConn = wsConn

	C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
}

func (h *AsgiRequestHandler) HandleWebsocketHeaders(statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	wsHeaders := h.w.Header().Clone()
	if headers != nil {
		mapHeaders := NewMapKeyValFromSource(headers)
		defer mapHeaders.Cleanup()

		for i := range mapHeaders.Len() {
			headerName, headerValue := mapHeaders.Get(i)
			wsHeaders.Add(headerName, headerValue)
		}
	}
	switch h.websocketState {
	case WS_STARTING:
		h.UpgradeWebsockets(wsHeaders, event)
	case WS_DISCONNECTED:
		C.AsgiEvent_websocket_set_disconnected(event)
		C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
	}
}

func (h *AsgiRequestHandler) HandleHeaders(statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		if headers != nil {
			mapHeaders := NewMapKeyValFromSource(headers)
			defer mapHeaders.Cleanup()

			for i := 0; i < mapHeaders.Len(); i++ {
				headerName, headerValue := mapHeaders.Get(i)
				h.w.Header().Add(headerName, headerValue)
			}
		}

		h.w.WriteHeader(int(statusCode))

		pythonMainThread.do(func() {
			C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		})
	}}
}

func (h *AsgiRequestHandler) SendResponse(body *C.char, bodyLen C.size_t, moreBody C.uint8_t, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		bodyBytes := C.GoBytes(unsafe.Pointer(body), C.int(bodyLen))
		h.accumulatedResponseSize += len(bodyBytes)
		_, err := h.w.Write(bodyBytes)
		if f, ok := h.w.(http.Flusher); ok {
			f.Flush()
		}
		if err != nil {
			h.done <- err
		} else if int(moreBody) == 0 {
			h.done <- nil
		}

		pythonMainThread.do(func() {
			C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		})
	}}
}

func (h *AsgiRequestHandler) SendResponseWebsocket(body *C.char, bodyLen C.size_t, messageType C.uint8_t, event *C.AsgiEvent) {
	h.operations <- AsgiOperations{op: func() {
		defer C.free(unsafe.Pointer(body))
		var bodyBytes []byte
		var wsMessageType int
		if messageType == C.uint8_t(0) {
			wsMessageType = websocket.TextMessage
			bodyBytes = []byte(C.GoString(body))
		} else {
			wsMessageType = websocket.BinaryMessage
			bodyBytes = C.GoBytes(unsafe.Pointer(body), C.int(bodyLen))
		}
		err := h.websocketConn.WriteMessage(wsMessageType, bodyBytes)
		if err != nil {
			h.websocketState = WS_DISCONNECTED
			h.websocketConn.Close()
			pythonMainThread.do(func() {
				C.AsgiEvent_websocket_set_disconnected(event)
				C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
			})
			return
		}

		pythonMainThread.do(func() {
			C.AsgiEvent_set(event, nil, 0, C.uint8_t(0), C.uint8_t(1))
		})
	}}
}

func (h *AsgiRequestHandler) CancelRequest() {
	h.done <- errors.New("request cancelled")
}

func (h *AsgiRequestHandler) CancelWebsocket(reason *C.char, code C.int) {
	var reasonText string
	if reason != nil {
		defer C.free(unsafe.Pointer(reason))
		reasonText = C.GoString(reason)
	}
	closeCode := int(code)
	if h.websocketState == WS_STARTING {
		h.w.WriteHeader(403)
		h.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
	} else if h.websocketState == WS_CONNECTED {
		h.websocketState = WS_DISCONNECTED
		closeMessage := websocket.FormatCloseMessage(closeCode, reasonText)
		go func() {
			if h.websocketConn != nil {
				h.websocketConn.WriteControl(websocket.CloseMessage, closeMessage, time.Now().Add(5*time.Second))
				h.websocketConn.Close()
				h.done <- fmt.Errorf("websocket closed: %d '%s'", closeCode, reasonText)
			}
		}()
	}
}

//export asgi_receive_start
func asgi_receive_start(requestID C.uint64_t, event *C.AsgiEvent) C.uint8_t {
	h := asgiState.GetHandler(uint64(requestID))
	if h == nil || h.completedResponse {
		return C.uint8_t(0)
	}
	h.SetEvent(event)

	if h.websocket {
		return h.HandleWebsocket(event)
	}

	return h.ReceiveStart(event)
}

//export asgi_set_headers
func asgi_set_headers(requestID C.uint64_t, statusCode C.int, headers *C.MapKeyVal, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	if h.websocket {
		h.HandleWebsocketHeaders(statusCode, headers, event)
		return
	}

	h.HandleHeaders(statusCode, headers, event)
}

//export asgi_send_response
func asgi_send_response(requestID C.uint64_t, body *C.char, bodyLen C.size_t, moreBody C.uint8_t, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	h.SendResponse(body, bodyLen, moreBody, event)
}

//export asgi_send_response_websocket
func asgi_send_response_websocket(requestID C.uint64_t, body *C.char, bodyLen C.size_t, messageType C.uint8_t, event *C.AsgiEvent) {
	h := asgiState.GetHandler(uint64(requestID))
	h.SetEvent(event)

	h.SendResponseWebsocket(body, bodyLen, messageType, event)
}

//export asgi_cancel_request
func asgi_cancel_request(requestID C.uint64_t) {
	h := asgiState.GetHandler(uint64(requestID))
	if h != nil {
		h.CancelRequest()
	}
}

//export asgi_cancel_request_websocket
func asgi_cancel_request_websocket(requestID C.uint64_t, reason *C.char, code C.int) {
	h := asgiState.GetHandler(uint64(requestID))
	if h != nil {
		h.CancelWebsocket(reason, code)
	}
}
