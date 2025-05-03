// Copyright 2024 <Miguel Liezun>
#ifndef CADDYSNAKE_H_
#define CADDYSNAKE_H_

#include <stdint.h>
#include <stdlib.h>

void Py_init_and_release_gil(const char *);

typedef struct {
  size_t count;
  char **keys;
  char **values;
} MapKeyVal;
MapKeyVal *MapKeyVal_new(size_t);
void MapKeyVal_free(MapKeyVal *map, size_t pos);

// WSGI Protocol
typedef struct WsgiApp WsgiApp;
WsgiApp *WsgiApp_import(const char *, const char *, const char *, const char *);
void WsgiApp_handle_request(WsgiApp *, int64_t, MapKeyVal *, const char *,
                            size_t);
void WsgiApp_cleanup(WsgiApp *);

extern void wsgi_write_response(int64_t, int, MapKeyVal *, char *, size_t);

// ASGI 3.0 protocol

typedef struct AsgiApp AsgiApp;
typedef struct AsgiEvent AsgiEvent;
AsgiApp *AsgiApp_import(const char *, const char *, const char *, const char *);
uint8_t AsgiApp_lifespan_startup(AsgiApp *);
uint8_t AsgiApp_lifespan_shutdown(AsgiApp *);
void AsgiApp_handle_request(AsgiApp *, uint64_t, MapKeyVal *, MapKeyVal *,
                            const char *, int, const char *, int, const char *);
void AsgiEvent_set(AsgiEvent *, const char *, size_t, uint8_t, uint8_t);
void AsgiEvent_set_websocket(AsgiEvent *, const char *, size_t, uint8_t,
                             uint8_t);
void AsgiEvent_connect_websocket(AsgiEvent *);
void AsgiEvent_disconnect_websocket(AsgiEvent *);
void AsgiEvent_cleanup(AsgiEvent *);
void AsgiApp_cleanup(AsgiApp *);

extern uint8_t asgi_receive_start(uint64_t, AsgiEvent *);
extern void asgi_send_response(uint64_t, char *, size_t, uint8_t, AsgiEvent *);
extern void asgi_send_response_websocket(uint64_t, char *, size_t, uint8_t,
                                         AsgiEvent *);
extern void asgi_set_headers(uint64_t, int, MapKeyVal *, AsgiEvent *);
extern void asgi_cancel_request(uint64_t);
extern void asgi_cancel_request_websocket(uint64_t, char *, int);

#endif // CADDYSNAKE_H_
