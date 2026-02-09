#include "caddysnake.h"
#include <Python.h>
#include <stdio.h>
#include <string.h>

#if PY_MAJOR_VERSION != 3 || PY_MINOR_VERSION < 10 || PY_MINOR_VERSION > 14
#error "This code requires Python 3.10, 3.11, 3.12, 3.13 or 3.14"
#endif

struct WsgiApp {
  PyObject *handler;
};

// WSGI: global variables
static PyObject *wsgi_version;
static PyObject *sys_stderr;
static PyObject *BytesIO;
static PyObject *task_queue_put;

// ASGI: global variables
static PyObject *asgi_version;
static PyObject *asyncio_Event_ts;
static PyObject *asyncio_Loop;
static PyObject *asyncio_run_coroutine_threadsafe;
static PyObject *build_receive;
static PyObject *build_send;
static PyObject *build_lifespan;
static PyObject *websocket_closed;

static char *copy_string(const char *str1) {
  size_t buffer_size = strlen(str1) + 1;
  char *result = malloc(buffer_size * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  memcpy(result, str1, buffer_size);
  return result;
}

static char *concatenate_buffers(const char *buffer1, size_t size1,
                                 const char *buffer2, size_t size2,
                                 size_t *out_size) {
  if (buffer1 == NULL || buffer2 == NULL || out_size == NULL) {
    return NULL;
  }
  size_t total_size = size1 + size2;
  char *result = malloc(total_size * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  memcpy(result, buffer1, size1);
  memcpy(result + size1, buffer2, size2);
  *out_size = total_size;
  return result;
}

static char *copy_pystring(PyObject *pystr) {
  Py_ssize_t og_size = 0;
  const char *og_str = PyUnicode_AsUTF8AndSize(pystr, &og_size);
  size_t new_str_len = og_size + 1;
  char *result = malloc(new_str_len * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  strcpy(result, og_str);
  return result;
}

static char *copy_pybytes(PyObject *pybytes, size_t *size) {
  Py_ssize_t og_size = 0;
  *size = 0;
  char *og_str;
  if (PyBytes_AsStringAndSize(pybytes, &og_str, &og_size) < 0) {
    return NULL;
  }
  char *result = malloc((og_size + 1) * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  memcpy(result, og_str, og_size);
  result[og_size] = '\0';
  *size = (size_t)og_size;
  return result;
}

MapKeyVal *MapKeyVal_new(size_t capacity) {
  MapKeyVal *new_map = (MapKeyVal *)malloc(sizeof(MapKeyVal));
  new_map->length = 0;
  new_map->capacity = capacity;
  new_map->keys = malloc(sizeof(char *) * capacity);
  new_map->values = malloc(sizeof(char *) * capacity);
  return new_map;
}

void MapKeyVal_free(MapKeyVal *map) {
  for (size_t i = 0; i < map->length; i++) {
    free(map->keys[i]);
    free(map->values[i]);
  }
  free(map->keys);
  free(map->values);
  free(map);
}

static void MapKeyVal_append(MapKeyVal *map, char *key, char *value) {
  map->keys[map->length] = key;
  map->values[map->length] = value;
  map->length++;
}

typedef struct {
  PyObject_HEAD WsgiApp *app;
  int64_t request_id;
  PyObject *request_environ;
  PyObject *response_headers;
  PyObject *response_body;
  int response_status;
} RequestResponse;

static void Debug_obj(PyObject *obj) {
  PyObject *repr = PyObject_Repr(obj);
  printf("%s\n", PyUnicode_AsUTF8(repr));
  Py_DECREF(repr);
}

static PyObject *Response_new(PyTypeObject *type, PyObject *args,
                              PyObject *kwds) {
  RequestResponse *self;
  self = (RequestResponse *)type->tp_alloc(type, 0);
  if (self != NULL) {
    self->request_id = 0;
    self->request_environ = NULL;
    self->response_headers = NULL;
    self->response_body = NULL;
    self->response_status = 500;
  }
  return (PyObject *)self;
}

static void Response_dealloc(RequestResponse *self) {
  Py_XDECREF(self->request_environ);
  Py_XDECREF(self->response_headers);
  Py_XDECREF(self->response_body);
  Py_TYPE(self)->tp_free((PyObject *)self);
}

static PyObject *Response_start(RequestResponse *self, PyObject *args) {
  PyObject *status;
  PyObject *response_headers;
  PyObject *exc_info = Py_None;

  if (!PyArg_ParseTuple(args, "OO|O", &status, &response_headers, &exc_info)) {
    PyErr_SetString(PyExc_RuntimeError, "input is invalid");
    Py_RETURN_NONE;
  }

  if (exc_info != Py_None && !PyTuple_Check(exc_info)) {
    PyErr_SetString(PyExc_RuntimeError, "exception info must be a tuple");
    Py_RETURN_NONE;
  }

  if (exc_info != Py_None) {
    if (!self->response_headers) {
      PyObject *type = NULL;
      PyObject *value = NULL;
      PyObject *traceback = NULL;

      if (!PyArg_ParseTuple(exc_info, "OOO", &type, &value, &traceback)) {
        PyErr_SetString(PyExc_RuntimeError, "exception info is invalid");
        Py_RETURN_NONE;
      }

      Py_INCREF(type);
      Py_INCREF(value);
      Py_INCREF(traceback);

      PyErr_Restore(type, value, traceback);

      Py_RETURN_NONE;
    }
  } else if (self->response_headers) {
    PyErr_SetString(PyExc_RuntimeError, "headers have already been sent");
    Py_RETURN_NONE;
  }

  self->response_status = (int)strtol(PyUnicode_AsUTF8(status), NULL, 10);
  self->response_headers = response_headers;
  Py_INCREF(self->response_headers);

  Py_RETURN_NONE;
}

static PyObject *Response_call_wsgi(RequestResponse *self, PyObject *args) {
  PyObject *start_response_fn =
      PyObject_GetAttrString((PyObject *)self, "start_response");
  PyObject *new_args = PyTuple_New(2);
  PyTuple_SetItem(new_args, 0, self->request_environ);
  PyTuple_SetItem(new_args, 1, start_response_fn);
  self->response_body = PyObject_Call(self->app->handler, new_args, NULL);
  Py_INCREF(self->request_environ);
  Py_DECREF(new_args);
  return (PyObject *)self;
}

static PyMethodDef Response_methods[] = {
    {"start_response", (PyCFunction)Response_start, METH_VARARGS,
     "Start the HTTP response by setting the status and headers."},
    {"call_wsgi", (PyCFunction)Response_call_wsgi, METH_VARARGS,
     "Call to start the WSGI App request handler."},
    {NULL} /* Sentinel */
};

static PyTypeObject ResponseType = {
    .ob_base = PyVarObject_HEAD_INIT(NULL, 0).tp_name =
        "caddysnake.RequestResponse",
    .tp_doc = PyDoc_STR("Request RequestResponse object"),
    .tp_basicsize = sizeof(RequestResponse),
    .tp_itemsize = 0,
    .tp_flags = Py_TPFLAGS_DEFAULT | Py_TPFLAGS_BASETYPE,
    .tp_new = Response_new,
    .tp_dealloc = (destructor)Response_dealloc,
    .tp_methods = Response_methods,
};

WsgiApp *WsgiApp_import(const char *module_name, const char *app_name,
                        const char *working_dir, const char *venv_path) {
  WsgiApp *app = malloc(sizeof(WsgiApp));
  if (app == NULL) {
    return NULL;
  }
  PyGILState_STATE gstate = PyGILState_Ensure();

  // Add working_dir into sys.path list
  if (working_dir) {
    PyObject *sysPath = PySys_GetObject("path");
    PyList_Append(sysPath, PyUnicode_FromString(working_dir));
  }

  // Add venv_path into sys.path list
  if (venv_path) {
    PyObject *sysPath = PySys_GetObject("path");
    PyList_Append(sysPath, PyUnicode_FromString(venv_path));
  }

  PyObject *module = PyImport_ImportModule(module_name);
  if (module == NULL) {
    PyErr_Print();
    PyGILState_Release(gstate);
    return NULL;
  }

  app->handler = PyObject_GetAttrString(module, app_name);
  if (!app->handler || !PyCallable_Check(app->handler)) {
    if (PyErr_Occurred()) {
      PyErr_Print();
    }
    PyGILState_Release(gstate);
    return NULL;
  }

  PyGILState_Release(gstate);
  return app;
}

void WsgiApp_cleanup(WsgiApp *app) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  Py_XDECREF(app->handler);
  PyGILState_Release(gstate);
  free(app);
}

void WsgiApp_handle_request(WsgiApp *app, int64_t request_id,
                            MapKeyVal *headers, const char *body,
                            size_t body_len) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *env_dict = PyDict_New();
  for (size_t i = 0; i < headers->length; i++) {
    PyObject *key = PyUnicode_FromString(headers->keys[i]);
    PyObject *value = PyUnicode_FromString(headers->values[i]);
    PyDict_SetItem(env_dict, key, value);
    Py_DECREF(key);
    Py_DECREF(value);
  }
  PyObject *input_key = PyUnicode_FromString("wsgi.input");
  PyObject *bytes = PyBytes_FromStringAndSize(body, body_len);
  PyObject *bytes_file = PyObject_CallOneArg(BytesIO, bytes);
  PyDict_SetItem(env_dict, input_key, bytes_file);
  Py_DECREF(input_key);
  Py_DECREF(bytes);
  Py_DECREF(bytes_file);

  char *extra_keys[] = {"wsgi.multithread", "wsgi.multiprocess",
                        "wsgi.run_once", "wsgi.version", "wsgi.errors"};
  PyObject *extra_values[] = {Py_True, Py_True, Py_False, wsgi_version,
                              sys_stderr};
  for (size_t i = 0; i < 5; i++) {
    PyObject *key = PyUnicode_FromString(extra_keys[i]);
    PyDict_SetItem(env_dict, key, extra_values[i]);
    Py_DECREF(key);
  }
  RequestResponse *r =
      (RequestResponse *)PyObject_CallObject((PyObject *)&ResponseType, NULL);
  r->app = app;
  r->request_id = request_id;
  r->request_environ = env_dict;
  PyObject_CallOneArg(task_queue_put, (PyObject *)r);

  PyGILState_Release(gstate);
}

static PyObject *response_callback(PyObject *self, PyObject *args) {
  RequestResponse *response = (RequestResponse *)PyTuple_GetItem(args, 0);
  PyObject *exc_info = PyTuple_GetItem(args, 1);
  if (exc_info != Py_None) {
    PyErr_Display(NULL, exc_info, NULL);
    goto finalize_error;
  }

  char *response_body = NULL;
  size_t response_body_size = 0;
  if (response->response_body) {
    PyObject *iterator = PyObject_GetIter(response->response_body);
    if (iterator) {
      PyObject *close_iterator = NULL;
      if (PyObject_HasAttrString(iterator, "close")) {
        close_iterator = PyObject_GetAttrString(iterator, "close");
      }
      PyObject *item;
      while ((item = PyIter_Next(iterator))) {
        if (!PyBytes_Check(item)) {
          PyErr_SetString(PyExc_RuntimeError,
                          "expected response body items to be bytes");
          PyErr_Print();
          Py_DECREF(item);
          if (close_iterator) {
            PyObject_CallNoArgs(close_iterator);
            Py_DECREF(close_iterator);
          }
          Py_DECREF(iterator);
          if (response_body != NULL) {
            free(response_body);
          }
          goto finalize_error;
        }
        char *previous_body = response_body;
        if (previous_body == NULL) {
          char *og_str;
          Py_ssize_t og_size;
          PyBytes_AsStringAndSize(item, &og_str, &og_size);
          response_body =
              concatenate_buffers("", 0, og_str, og_size, &response_body_size);
        } else {
          char *og_str;
          Py_ssize_t og_size;
          PyBytes_AsStringAndSize(item, &og_str, &og_size);
          response_body =
              concatenate_buffers(previous_body, response_body_size, og_str,
                                  og_size, &response_body_size);
          free(previous_body);
        }
        Py_DECREF(item);
      }
      if (close_iterator) {
        PyObject_CallNoArgs(close_iterator);
        Py_DECREF(close_iterator);
      }
      Py_DECREF(iterator);
    } else {
      PyErr_Print();
      goto finalize_error;
    }
  } else {
    PyErr_SetString(PyExc_RuntimeError,
                    "expected response body to be non-empty");
    PyErr_Print();
    goto finalize_error;
  }

  if (PyErr_Occurred()) {
    PyErr_Print();
    if (response_body != NULL) {
      free(response_body);
    }
    goto finalize_error;
  }

  if (!response->response_headers) {
    PyErr_SetString(PyExc_RuntimeError,
                    "expected response headers to be non-empty");
    PyErr_Print();
    if (response_body != NULL) {
      free(response_body);
    }
    goto finalize_error;
  }
  PyObject *iterator = PyObject_GetIter(response->response_headers);
  if (!iterator) {
    PyErr_Print();
    if (response_body != NULL) {
      free(response_body);
    }
    goto finalize_error;
  }
  Py_ssize_t headers_count = 0;
  if (PyTuple_Check(response->response_headers)) {
    headers_count = PyTuple_Size(response->response_headers);
  } else if (PyList_Check(response->response_headers)) {
    headers_count = PyList_Size(response->response_headers);
  } else {
    PyErr_SetString(PyExc_RuntimeError,
                    "response headers is not list or tuple");
    PyErr_Print();
    Py_DECREF(iterator);
    if (response_body != NULL) {
      free(response_body);
    }
    goto finalize_error;
  }

  MapKeyVal *http_headers = MapKeyVal_new(headers_count);

  PyObject *key, *value, *item;
  while ((item = PyIter_Next(iterator))) {
    if (!PyTuple_Check(item) || PyTuple_Size(item) != 2) {
      PyErr_SetString(PyExc_RuntimeError,
                      "expected response headers to be tuples with 2 items");
      PyErr_Print();
      Py_DECREF(item);
      Py_DECREF(iterator);
      MapKeyVal_free(http_headers);
      goto finalize_error;
    }
    key = PyTuple_GetItem(item, 0);
    value = PyTuple_GetItem(item, 1);
    MapKeyVal_append(http_headers, copy_pystring(key), copy_pystring(value));
    Py_DECREF(item);
  }
  Py_DECREF(iterator);

  Py_BEGIN_ALLOW_THREADS wsgi_write_response(
      response->request_id, response->response_status, http_headers,
      response_body, response_body_size);
  Py_END_ALLOW_THREADS goto end;

finalize_error:
  Py_BEGIN_ALLOW_THREADS wsgi_write_response(response->request_id, 500, NULL,
                                             NULL, 0);
  Py_END_ALLOW_THREADS

      end : Py_RETURN_NONE;
}

static PyMethodDef CaddysnakeMethods[] = {
    {"response_callback", response_callback, METH_VARARGS,
     "Callback to process response."},
    {NULL, NULL, 0, NULL} /* Sentinel */
};

static struct PyModuleDef CaddysnakeModule = {
    PyModuleDef_HEAD_INIT, "caddysnake", NULL, -1, CaddysnakeMethods,
};

// ASGI 3.0 protocol implementation
struct AsgiApp {
  PyObject *handler;
  PyObject *state;

  PyObject *lifespan_startup;
  PyObject *lifespan_shutdown;
};

AsgiApp *AsgiApp_import(const char *module_name, const char *app_name,
                        const char *working_dir, const char *venv_path) {
  AsgiApp *app = malloc(sizeof(AsgiApp));
  if (app == NULL) {
    return NULL;
  }
  app->lifespan_startup = NULL;
  app->lifespan_shutdown = NULL;
  PyGILState_STATE gstate = PyGILState_Ensure();

  // Add working_dir into sys.path list
  if (working_dir) {
    PyObject *sysPath = PySys_GetObject("path");
    PyList_Append(sysPath, PyUnicode_FromString(working_dir));
  }

  // Add venv_path into sys.path list
  if (venv_path) {
    PyObject *sysPath = PySys_GetObject("path");
    PyList_Append(sysPath, PyUnicode_FromString(venv_path));
  }

  PyObject *module = PyImport_ImportModule(module_name);
  if (module == NULL) {
    PyErr_Print();
    PyGILState_Release(gstate);
    return NULL;
  }

  app->handler = PyObject_GetAttrString(module, app_name);
  if (!app->handler || !PyCallable_Check(app->handler)) {
    if (PyErr_Occurred()) {
      PyErr_Print();
    }
    PyGILState_Release(gstate);
    return NULL;
  }
  app->state = PyDict_New();

  PyGILState_Release(gstate);
  return app;
}

uint8_t AsgiApp_lifespan_startup(AsgiApp *app) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *args = PyTuple_New(2);
  PyTuple_SetItem(args, 0, app->handler);
  PyTuple_SetItem(args, 1, app->state);
  PyObject *result = PyObject_Call(build_lifespan, args, NULL);
  Py_DECREF(args);

  app->lifespan_startup = PyTuple_GetItem(result, 0);
  app->lifespan_shutdown = PyTuple_GetItem(result, 1);

  result = PyObject_CallNoArgs(app->lifespan_startup);

  uint8_t status = result == Py_True;

  PyGILState_Release(gstate);

  return status;
}

uint8_t AsgiApp_lifespan_shutdown(AsgiApp *app) {
  if (app->lifespan_shutdown == NULL) {
    return 1;
  }

  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *result = PyObject_CallNoArgs(app->lifespan_shutdown);

  uint8_t status = result == Py_True;

  PyGILState_Release(gstate);

  return status;
}

struct AsgiEvent {
  PyObject_HEAD AsgiApp *app;
  uint64_t request_id;
  PyObject *event_ts_send;
  PyObject *event_ts_receive;
  PyObject *future;
  PyObject *request_body;
  uint8_t more_body;
  uint8_t websockets_state;
};

#define WS_NONE 0
#define WS_CONNECTED 1
#define WS_DISCONNECTED 2

static PyObject *AsgiEvent_new(PyTypeObject *type, PyObject *args,
                               PyObject *kwds) {
  AsgiEvent *self;
  self = (AsgiEvent *)type->tp_alloc(type, 0);
  if (self != NULL) {
    self->request_id = 0;
    self->event_ts_send = NULL;
    self->event_ts_receive = NULL;
    self->future = NULL;
    self->request_body = NULL;
    self->more_body = 0;
    self->websockets_state = WS_NONE;
  }
  return (PyObject *)self;
}

static void AsgiEvent_dealloc(AsgiEvent *self) {
  Py_XDECREF(self->event_ts_send);
  Py_XDECREF(self->event_ts_receive);
  // Future is freed in AsgiEvent_result
  // Py_XDECREF(self->future);
  // Request body is also freed in AsgiEvent_set
  Py_XDECREF(self->request_body);
  Py_TYPE(self)->tp_free((PyObject *)self);
}

void AsgiEvent_cleanup(AsgiEvent *event) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  Py_DECREF(event);
  PyGILState_Release(gstate);
}

void AsgiEvent_set(AsgiEvent *self, const char *body, size_t body_len,
                   uint8_t more_body, uint8_t is_send) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  if (body) {
    if (self->request_body) {
      Py_DECREF(self->request_body);
    }
    self->request_body = PyBytes_FromStringAndSize(body, body_len);
  }
  self->more_body = more_body;
  PyObject *set_fn = NULL;
  if (is_send) {
    set_fn = PyObject_GetAttrString((PyObject *)self->event_ts_send, "set");
  } else {
    set_fn = PyObject_GetAttrString((PyObject *)self->event_ts_receive, "set");
  }
  PyObject_CallNoArgs(set_fn);
  Py_DECREF(set_fn);
  PyGILState_Release(gstate);
}

void AsgiEvent_set_websocket(AsgiEvent *self, const char *body, size_t body_len,
                             uint8_t message_type, uint8_t is_send) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  if (body_len) {
    if (!self->request_body) {
      self->request_body = PyList_New(0);
    }
    PyObject *tuple = PyTuple_New(2);
    if (message_type == 0) {
      PyTuple_SetItem(tuple, 0, PyUnicode_FromStringAndSize(body, body_len));
    } else {
      PyTuple_SetItem(tuple, 0, PyBytes_FromStringAndSize(body, body_len));
    }
    PyTuple_SetItem(tuple, 1, PyLong_FromLong(message_type));
    PyList_Append(self->request_body, tuple);
    Py_DECREF(tuple);
  }
  PyObject *set_fn = NULL;
  if (is_send) {
    set_fn = PyObject_GetAttrString((PyObject *)self->event_ts_send, "set");
  } else {
    set_fn = PyObject_GetAttrString((PyObject *)self->event_ts_receive, "set");
  }
  PyObject_CallNoArgs(set_fn);
  Py_DECREF(set_fn);
  PyGILState_Release(gstate);
}

void AsgiEvent_websocket_set_connected(AsgiEvent *self) {
  self->websockets_state = WS_CONNECTED;
}

void AsgiEvent_websocket_set_disconnected(AsgiEvent *self) {
  self->websockets_state = WS_DISCONNECTED;
}

static void AsgiEvent_http_read_body(AsgiEvent *self, PyObject *data) {
  PyObject *data_type = PyUnicode_FromString("http.request");
  PyDict_SetItemString(data, "type", data_type);
  PyDict_SetItemString(data, "body", self->request_body);
  PyObject *more_body = Py_False;
  if (self->more_body) {
    more_body = Py_True;
  }
  PyDict_SetItemString(data, "more_body", more_body);
  Py_DECREF(data_type);
}

static void AsgiEvent_websocket_receive(AsgiEvent *self, PyObject *data) {
  PyObject *data_type = PyUnicode_FromString("websocket.receive");
  PyDict_SetItemString(data, "type", data_type);
  PyObject *pop_fn = PyObject_GetAttrString(self->request_body, "pop");
  PyObject *ix = PyLong_FromLong(0);
  PyObject *message = PyObject_CallOneArg(pop_fn, ix);
  PyObject *message_data = PyTuple_GetItem(message, 0);
  PyObject *message_type = PyTuple_GetItem(message, 1);
  if (message_type == ix) {
    PyDict_SetItemString(data, "text", message_data);
  } else {
    PyDict_SetItemString(data, "bytes", message_data);
  }
  Py_DECREF(message);
  Py_DECREF(ix);
  Py_DECREF(pop_fn);
  Py_DECREF(data_type);
}

static void AsgiEvent_websocket_connect(AsgiEvent *self, PyObject *data) {
  PyObject *data_type = PyUnicode_FromString("websocket.connect");
  PyDict_SetItemString(data, "type", data_type);
  Py_DECREF(data_type);
}

static void AsgiEvent_websocket_disconnect(AsgiEvent *self, PyObject *data) {
  PyObject *data_type = PyUnicode_FromString("websocket.disconnect");
  PyDict_SetItemString(data, "type", data_type);
  Py_DECREF(data_type);
  PyObject *default_code = PyLong_FromLong(1005);
  PyObject *close_code = default_code;
  if (self->request_body && PyList_Size(self->request_body) > 0) {
    PyObject *pop_fn = PyObject_GetAttrString(self->request_body, "pop");
    PyObject *ix = PyLong_FromLong(0);
    PyObject *message = PyObject_CallOneArg(pop_fn, ix);
    PyObject *message_data = PyTuple_GetItem(message, 0);
    PyObject *message_type = PyTuple_GetItem(message, 1);
    if (message_type == ix) {
      close_code = PyLong_FromUnicodeObject(message_data, 10);
      if (!close_code) {
        if (PyErr_Occurred()) {
          PyErr_Clear();
        }
        close_code = default_code;
      }
    }
    Py_DECREF(message);
    Py_DECREF(ix);
    Py_DECREF(pop_fn);
  }
  PyDict_SetItemString(data, "code", close_code);
  if (close_code != default_code) {
    Py_DECREF(close_code);
  }
  Py_DECREF(default_code);
}

static PyObject *AsgiEvent_receive_start(AsgiEvent *self, PyObject *args) {
  PyObject *result = Py_False;
  if (asgi_receive_start(self->request_id, self) == 1) {
    Py_INCREF(self->event_ts_receive);
    result = self->event_ts_receive;
  }
#if PY_MINOR_VERSION < 12
  if (result == Py_False)
    Py_INCREF(result);
#endif
  return result;
}

static PyObject *AsgiEvent_receive_end(AsgiEvent *self, PyObject *args) {
  PyObject *data = PyDict_New();
  switch (self->websockets_state) {
  case WS_NONE: {
    AsgiEvent_http_read_body(self, data);
    break;
  }

  case WS_CONNECTED: {
    if (!self->request_body) {
      AsgiEvent_websocket_connect(self, data);
    } else {
      AsgiEvent_websocket_receive(self, data);
    }
    break;
  }

  case WS_DISCONNECTED: {
    AsgiEvent_websocket_disconnect(self, data);
    break;
  }
  }
  return data;
}

uint8_t is_weboscket_closed(PyObject *exc) {
  if (PyErr_GivenExceptionMatches(exc, websocket_closed)) {
    return 1;
  }
  PyObject *cause = PyObject_GetAttrString(exc, "__cause__");
  if (cause) {
    if (PyErr_GivenExceptionMatches(cause, websocket_closed)) {
      Py_DECREF(cause);
      return 1;
    }
    Py_DECREF(cause);
  }
  return 0;
}

/*
AsgiEvent_result is called when an execution of AsgiApp finishes.
*/
static PyObject *AsgiEvent_result(AsgiEvent *self, PyObject *args) {
  PyObject *future_exception =
      PyObject_GetAttrString(self->future, "exception");
  PyObject *exc = PyObject_CallNoArgs(future_exception);
  if (exc != Py_None) {
    if (!is_weboscket_closed(exc)) {
#if PY_MINOR_VERSION >= 12
      // PyErr_DisplayException was introduced in Python 3.12
      PyErr_DisplayException(exc);
#else
      PyErr_Display(NULL, exc, NULL);
#endif
      if (self->websockets_state == WS_NONE) {
        asgi_cancel_request(self->request_id);
      } else {
        asgi_cancel_request_websocket(self->request_id, NULL, 1000);
      }
    }
    Py_DECREF(exc);
  }
  Py_DECREF(future_exception);

  // Freeing future here because there is a circular reference
  // between AsgiEvent and Future.
  Py_DECREF(self->future);
  self->future = NULL;

  Py_RETURN_NONE;
}

static void AsgiEvent_response_start(AsgiEvent *self, PyObject *data) {
  PyObject *status_code = PyDict_GetItemString(data, "status");
  PyObject *headers = PyDict_GetItemString(data, "headers");

  PyObject *iterator = PyObject_GetIter(headers);
  Py_ssize_t headers_count = 0;
  if (PyTuple_Check(headers)) {
    headers_count = PyTuple_Size(headers);
  } else if (PyList_Check(headers)) {
    headers_count = PyList_Size(headers);
  }
  MapKeyVal *http_headers = MapKeyVal_new(headers_count);

  PyObject *key, *value, *item;
  size_t len = 0;
  while ((item = PyIter_Next(iterator))) {
    // if (!PyTuple_Check(item) || PyTuple_Size(item) != 2) {
    //   PyErr_SetString(PyExc_RuntimeError,
    //                   "expected response headers to be tuples with 2
    //                   items");
    //   PyErr_Print();
    //   Py_DECREF(item);
    //   Py_DECREF(iterator);
    //   MapKeyVal_free(http_headers, pos);
    //   goto finalize_error;
    // }
    key = PyTuple_GetItem(item, 0);
    value = PyTuple_GetItem(item, 1);
    MapKeyVal_append(http_headers, copy_pybytes(key, &len),
                     copy_pybytes(value, &len));
    Py_DECREF(item);
  }
  Py_DECREF(iterator);

  asgi_set_headers(self->request_id, PyLong_AsLong(status_code), http_headers,
                   self);
}

static void AsgiEvent_write_body(AsgiEvent *self, PyObject *data) {
  PyObject *more_body = PyDict_GetItemString(data, "more_body");
  uint8_t send_more_body = 1;
  if (!more_body || PyObject_RichCompareBool(more_body, Py_False, Py_EQ) == 1) {
    send_more_body = 0;
  }
  PyObject *pybody = PyDict_GetItemString(data, "body");
  size_t body_len = 0;
  char *body = copy_pybytes(pybody, &body_len);
  asgi_send_response(self->request_id, body, body_len, send_more_body, self);
}

static void AsgiEvent_websocket_accept(AsgiEvent *self, PyObject *data) {
  PyObject *headers = PyDict_GetItemString(data, "headers");
  PyObject *subprotocol = PyDict_GetItemString(data, "subprotocol");

  Py_ssize_t headers_count = 0;
  PyObject *iterator = NULL;
  if (headers) {
    iterator = PyObject_GetIter(headers);
    if (PyTuple_Check(headers)) {
      headers_count = PyTuple_Size(headers);
    } else if (PyList_Check(headers)) {
      headers_count = PyList_Size(headers);
    }
  }
  if (subprotocol && subprotocol != Py_None) {
    headers_count += 1;
  }

  MapKeyVal *http_headers = MapKeyVal_new(headers_count);
  size_t len = 0;

  if (iterator) {
    PyObject *key, *value, *item;
    while ((item = PyIter_Next(iterator))) {
      // if (!PyTuple_Check(item) || PyTuple_Size(item) != 2) {
      //   PyErr_SetString(PyExc_RuntimeError,
      //                   "expected response headers to be tuples with 2
      //                   items");
      //   PyErr_Print();
      //   Py_DECREF(item);
      //   Py_DECREF(iterator);
      //   MapKeyVal_free(http_headers, pos);
      //   goto finalize_error;
      // }
      key = PyTuple_GetItem(item, 0);
      value = PyTuple_GetItem(item, 1);
      MapKeyVal_append(http_headers, copy_pybytes(key, &len),
                       copy_pybytes(value, &len));
      Py_DECREF(item);
    }
    Py_DECREF(iterator);
  }

  if (subprotocol && subprotocol != Py_None) {
    MapKeyVal_append(http_headers, copy_string("sec-websocket-protocol"),
                     copy_pybytes(subprotocol, &len));
  }

  asgi_set_headers(self->request_id, 101, http_headers, self);
}

static void AsgiEvent_websocket_write_response(AsgiEvent *self,
                                               PyObject *data) {
  PyObject *data_text = PyDict_GetItemString(data, "text");
  char *body = NULL;
  size_t body_len = 0;
  uint8_t message_type = 0;
  if (data_text) {
    body = copy_pystring(data_text);
    message_type = 0;
  } else {
    body = copy_pybytes(PyDict_GetItemString(data, "bytes"), &body_len);
    message_type = 1;
  }
  asgi_send_response_websocket(self->request_id, body, body_len, message_type,
                               self);
}

static void AsgiEvent_websocket_close(AsgiEvent *self, PyObject *data) {
  PyObject *close_code = PyDict_GetItemString(data, "code");
  PyObject *close_reason = PyDict_GetItemString(data, "reason");
  int code = 1000;
  if (close_code) {
    code = PyLong_AsLong(close_code);
  }
  char *reason = NULL;
  if (close_reason) {
    reason = copy_pystring(close_reason);
  }

  asgi_cancel_request_websocket(self->request_id, reason, code);
}

static PyObject *AsgiEvent_send(AsgiEvent *self, PyObject *args) {
  PyObject *data = PyTuple_GetItem(args, 0);
  PyObject *data_type = PyDict_GetItemString(data, "type");
  if (PyUnicode_CompareWithASCIIString(data_type, "http.response.start") == 0) {
    AsgiEvent_response_start(self, data);
  } else if (PyUnicode_CompareWithASCIIString(data_type,
                                              "http.response.body") == 0) {
    AsgiEvent_write_body(self, data);
  } else if (PyUnicode_CompareWithASCIIString(data_type, "websocket.accept") ==
             0) {
    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
    AsgiEvent_websocket_accept(self, data);
    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
  } else if (PyUnicode_CompareWithASCIIString(data_type, "websocket.send") ==
             0) {

    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
    AsgiEvent_websocket_write_response(self, data);
    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
  } else if (PyUnicode_CompareWithASCIIString(data_type, "websocket.close") ==
             0) {

    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
    AsgiEvent_websocket_close(self, data);
    if (self->websockets_state == WS_DISCONNECTED) {
      goto websocket_error;
    }
  }
  goto finalize_send;

  PyObject *exc_instance;
websocket_error:
  exc_instance = PyObject_CallObject(websocket_closed, NULL);
  PyErr_SetObject(websocket_closed, exc_instance);
  Py_DECREF(exc_instance);
  Py_RETURN_NONE;

finalize_send:
  Py_INCREF(self->event_ts_send);
  return self->event_ts_send;
}

static PyMethodDef AsgiEvent_methods[] = {
    {"receive_start", (PyCFunction)AsgiEvent_receive_start, METH_VARARGS,
     "Start reading receive data."},
    {"receive_end", (PyCFunction)AsgiEvent_receive_end, METH_VARARGS,
     "Return all received data."},
    {"send", (PyCFunction)AsgiEvent_send, METH_VARARGS,
     "Send data back to client."},
    {"result", (PyCFunction)AsgiEvent_result, METH_VARARGS,
     "Called when the Future has finished."},
    {NULL} /* Sentinel */
};

static PyTypeObject AsgiEventType = {
    .ob_base = PyVarObject_HEAD_INIT(NULL, 0).tp_name = "caddysnake.AsgiEvent",
    .tp_doc = PyDoc_STR("ASGI Event object"),
    .tp_basicsize = sizeof(AsgiEvent),
    .tp_itemsize = 0,
    .tp_flags = Py_TPFLAGS_DEFAULT | Py_TPFLAGS_BASETYPE,
    .tp_new = AsgiEvent_new,
    .tp_dealloc = (destructor)AsgiEvent_dealloc,
    .tp_methods = AsgiEvent_methods,
};

void AsgiApp_handle_request(AsgiApp *app, uint64_t request_id, MapKeyVal *scope,
                            MapKeyVal *headers, const char *client_host,
                            int client_port, const char *server_host,
                            int server_port, const char *subprotocols) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *scope_dict = PyDict_New();
  PyDict_SetItemString(scope_dict, "asgi", asgi_version);

  for (int i = 0; i < scope->length; i++) {
    const char *key = scope->keys[i];
    if (strcmp(key, "raw_path") == 0 || strcmp(key, "query_string") == 0) {
      PyObject *value = PyBytes_FromString(scope->values[i]);
      PyDict_SetItemString(scope_dict, key, value);

      Py_DECREF(value);
    } else {
      PyObject *value = PyUnicode_FromString(scope->values[i]);
      PyDict_SetItemString(scope_dict, key, value);

      Py_DECREF(value);
    }
  }

  PyObject *headers_tuple = PyTuple_New(headers->length);
  for (int i = 0; i < headers->length; i++) {
    PyObject *element = PyTuple_New(2);
    PyTuple_SetItem(element, 0, PyBytes_FromString(headers->keys[i]));
    PyTuple_SetItem(element, 1, PyBytes_FromString(headers->values[i]));
    PyTuple_SetItem(headers_tuple, i, element);
  }
  PyDict_SetItemString(scope_dict, "headers", headers_tuple);
  Py_DECREF(headers_tuple);

  PyObject *client_tuple = PyTuple_New(2);
  PyTuple_SetItem(client_tuple, 0, PyUnicode_FromString(client_host));
  PyTuple_SetItem(client_tuple, 1, PyLong_FromLong(client_port));
  PyDict_SetItemString(scope_dict, "client", client_tuple);
  Py_DECREF(client_tuple);

  PyObject *server_tuple = PyTuple_New(2);
  PyTuple_SetItem(server_tuple, 0, PyUnicode_FromString(server_host));
  PyTuple_SetItem(server_tuple, 1, PyLong_FromLong(server_port));
  PyDict_SetItemString(scope_dict, "server", server_tuple);
  Py_DECREF(server_tuple);

  PyObject *state = PyDict_Copy(app->state);
  PyDict_SetItemString(scope_dict, "state", state);
  Py_DECREF(state);

  if (subprotocols) {
    PyObject *py_subprotocols = PyUnicode_FromString(subprotocols);
    PyObject *split_list =
        PyObject_CallMethod(py_subprotocols, "split", "s", ",");
    if (!split_list) {
      if (PyErr_Occurred()) {
        PyErr_Clear();
      }
    } else {
      PyDict_SetItemString(scope_dict, "subprotocols", split_list);
      Py_DECREF(split_list);
    }
    Py_DECREF(py_subprotocols);
  }

  AsgiEvent *asgi_event =
      (AsgiEvent *)PyObject_CallObject((PyObject *)&AsgiEventType, NULL);
  asgi_event->app = app;
  asgi_event->request_id = request_id;
  asgi_event->event_ts_send = PyObject_CallNoArgs(asyncio_Event_ts);
  asgi_event->event_ts_receive = PyObject_CallNoArgs(asyncio_Event_ts);

  PyObject *receive =
      PyObject_CallOneArg(build_receive, (PyObject *)asgi_event);
  PyObject *send = PyObject_CallOneArg(build_send, (PyObject *)asgi_event);

  PyObject *args = PyTuple_New(3);
  PyTuple_SetItem(args, 0, scope_dict);
  PyTuple_SetItem(args, 1, receive);
  PyTuple_SetItem(args, 2, send);
  PyObject *coro = PyObject_Call(app->handler, args, NULL);
  Py_DECREF(args);

  Py_INCREF(asyncio_Loop);
  args = PyTuple_New(2);
  PyTuple_SetItem(args, 0, coro);
  PyTuple_SetItem(args, 1, asyncio_Loop);
  asgi_event->future =
      PyObject_Call(asyncio_run_coroutine_threadsafe, args, NULL);
  Py_DECREF(args);

  PyObject *add_done_callback =
      PyObject_GetAttrString(asgi_event->future, "add_done_callback");
  PyObject *asgi_event_result =
      PyObject_GetAttrString((PyObject *)asgi_event, "result");
  PyObject_CallOneArg(add_done_callback, asgi_event_result);
  Py_DECREF(add_done_callback);
  Py_DECREF(asgi_event_result);

  PyGILState_Release(gstate);
}

void AsgiApp_cleanup(AsgiApp *app) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  Py_XDECREF(app->handler);
  Py_XDECREF(app->state);
  Py_XDECREF(app->lifespan_startup);
  Py_XDECREF(app->lifespan_shutdown);
  PyGILState_Release(gstate);
  free(app);
}

// Autoreload: Invalidate Python module cache

void Py_invalidate_module_cache(const char *working_dir) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *sys_modules = PySys_GetObject("modules");
  if (!sys_modules) {
    PyGILState_Release(gstate);
    return;
  }

  // Get a copy of the keys to avoid modifying the dict during iteration
  PyObject *keys = PyDict_Keys(sys_modules);
  if (!keys) {
    PyGILState_Release(gstate);
    return;
  }

  size_t working_dir_len = strlen(working_dir);
  Py_ssize_t n = PyList_Size(keys);

  for (Py_ssize_t i = 0; i < n; i++) {
    PyObject *key = PyList_GetItem(keys, i);
    PyObject *module = PyDict_GetItem(sys_modules, key);

    if (!module)
      continue;

    PyObject *file_attr = PyObject_GetAttrString(module, "__file__");
    if (!file_attr || file_attr == Py_None) {
      if (PyErr_Occurred())
        PyErr_Clear();
      Py_XDECREF(file_attr);
      continue;
    }

    if (PyUnicode_Check(file_attr)) {
      const char *file_path = PyUnicode_AsUTF8(file_attr);
      if (file_path && strncmp(file_path, working_dir, working_dir_len) == 0) {
        PyDict_DelItem(sys_modules, key);
      }
    }

    Py_DECREF(file_attr);
  }

  Py_DECREF(keys);
  PyGILState_Release(gstate);
}

// Initialization

void Py_init_and_release_gil(const char *setup_py) {
  PyStatus status;
  PyConfig config;
  PyConfig_InitPythonConfig(&config);
  // Set the program name. Implicitly preinitialize Python
  status = PyConfig_SetString(&config, &config.program_name, L"caddysnake");
  if (PyStatus_Exception(status)) {
    goto exception;
  }
  status = Py_InitializeFromConfig(&config);
  if (PyStatus_Exception(status)) {
    goto exception;
  }
  PyConfig_Clear(&config);

  // Configure python path to recognize modules in the current directory
  PyObject *sysPath = PySys_GetObject("path");
  PyList_Insert(sysPath, 0, PyUnicode_FromString(""));

  // Used for turning bytes-like object into a file-like object
  PyObject *io_module = PyImport_ImportModule("io");
  BytesIO = PyObject_GetAttrString(io_module, "BytesIO");

  // Used for events
  PyObject *asyncio = PyImport_ImportModule("asyncio");
  PyObject *loop_name = PyUnicode_FromString("new_event_loop");
  asyncio_Loop = PyObject_CallMethodNoArgs(asyncio, loop_name);
  Py_DECREF(loop_name);
  asyncio_run_coroutine_threadsafe =
      PyObject_GetAttrString(asyncio, "run_coroutine_threadsafe");

  PyObject *caddysnake_module = PyModule_Create(&CaddysnakeModule);
  PyObject *response_callback_fn =
      PyObject_GetAttrString(caddysnake_module, "response_callback");

  // Initialize types
  PyType_Ready(&ResponseType);
  PyType_Ready(&AsgiEventType);

  // Create setup functions, see file: caddysnake.py
  PyRun_SimpleString(setup_py);
  PyObject *main_module = PyImport_AddModule("__main__");

  // WSGI: Setup task queue and consumer threads
  PyObject *wsgi_setup_fn =
      PyObject_GetAttrString(main_module, "caddysnake_setup_wsgi");
  PyObject *task_queue =
      PyObject_CallOneArg(wsgi_setup_fn, response_callback_fn);
  task_queue_put = PyObject_GetAttrString(task_queue, "put");
  PyRun_SimpleString("del caddysnake_setup_wsgi");
  // Setup WSGI version
  wsgi_version = PyTuple_New(2);
  PyTuple_SetItem(wsgi_version, 0, PyLong_FromLong(1));
  PyTuple_SetItem(wsgi_version, 1, PyLong_FromLong(0));

  // Setup stderr for logging
  sys_stderr = PySys_GetObject("stderr");

  // ASGI: Setup wrappers for asyncio events
  PyObject *asgi_setup_fn =
      PyObject_GetAttrString(main_module, "caddysnake_setup_asgi");
  PyObject *asgi_setup_result =
      PyObject_CallOneArg(asgi_setup_fn, asyncio_Loop);
  asyncio_Event_ts = PyTuple_GetItem(asgi_setup_result, 0);
  build_receive = PyTuple_GetItem(asgi_setup_result, 1);
  build_send = PyTuple_GetItem(asgi_setup_result, 2);
  build_lifespan = PyTuple_GetItem(asgi_setup_result, 3);
  websocket_closed = PyTuple_GetItem(asgi_setup_result, 4);
  PyRun_SimpleString("del caddysnake_setup_asgi");
  // Setup ASGI version
  asgi_version = PyDict_New();
  PyDict_SetItemString(asgi_version, "version", PyUnicode_FromString("3.0"));
  PyDict_SetItemString(asgi_version, "spec_version",
                       PyUnicode_FromString("2.3"));

  // This are global objects expected to exist during the entire program
  // lifetime. Refcounts can be safely decreased, but there's no need to do it
  // because we expect the objects to stick around forever.
  // Py_DECREF(task_queue);
  // Py_DECREF(wsgi_setup_fn);
  // Py_DECREF(response_callback_fn);
  // Py_DECREF(io_module);
  // Py_DECREF(caddysnake_module);

  PyEval_ReleaseThread(PyGILState_GetThisThreadState());
  return;

exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}
