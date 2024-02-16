#ifndef _WSGI_H
#define _WSGI_H

#include <stdint.h>
#include <stdlib.h>

void Py_init_and_release_gil();

typedef struct {
    size_t count;
    char** keys;
    char** values;
} HTTPHeaders;
HTTPHeaders* HTTPHeaders_new(size_t);

int App_import(const char*, const char*);
void App_handle_request(int64_t, HTTPHeaders*, const char*);

extern void go_callback(int64_t, int, HTTPHeaders*, char*);

#endif
