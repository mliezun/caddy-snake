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

// WSGI Protocol
typedef struct WsgiApp WsgiApp;
WsgiApp *WsgiApp_import(const char *, const char *, const char *);
void WsgiApp_handle_request(WsgiApp *, int64_t, MapKeyVal *, const char *);
void WsgiApp_cleanup(WsgiApp *);

extern void wsgi_write_response(int64_t, int, MapKeyVal *, char *);

// ASGI 3.0 protocol

typedef struct AsgiApp AsgiApp;
typedef struct AsgiEvent AsgiEvent;
AsgiApp *AsgiApp_import(const char *, const char *, const char *);
void AsgiApp_handle_request(AsgiApp *, uint64_t, MapKeyVal *, MapKeyVal *,
                            const char *, int, const char *, int);
void AsgiEvent_set(AsgiEvent *, const char *);

extern void asgi_receive_start(uint64_t, AsgiEvent *);
extern void asgi_add_response(uint64_t, char *);
extern void asgi_set_headers(uint64_t, int, MapKeyVal *, AsgiEvent *);
extern void asgi_send_response(uint64_t, AsgiEvent *);

#endif // CADDYSNAKE_H_
