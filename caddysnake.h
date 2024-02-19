#ifndef _WSGI_H
#define _WSGI_H

#include <stdint.h>
#include <stdlib.h>

typedef struct WsgiApp WsgiApp;

void Py_init_and_release_gil();

typedef struct {
    size_t count;
    char** keys;
    char** values;
} HTTPHeaders;
HTTPHeaders* HTTPHeaders_new(size_t);

WsgiApp* App_import(const char*, const char*);
void App_handle_request(WsgiApp*, int64_t, HTTPHeaders*, const char*);
void App_cleanup(WsgiApp*);

extern void go_callback(int64_t, int, HTTPHeaders*, char*);

#endif
