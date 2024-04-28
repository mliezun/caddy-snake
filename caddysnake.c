#include "caddysnake.h"
#include <Python.h>
#include <stdio.h>
#include <string.h>

#if PY_MAJOR_VERSION != 3 || PY_MINOR_VERSION < 9 || PY_MINOR_VERSION > 12
#error "This code requires Python 3.9, 3.10, 3.11 or 3.12"
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

char *concatenate_strings(const char *str1, const char *str2) {
  size_t new_str_len = strlen(str1) + strlen(str2) + 1;
  char *result = malloc(new_str_len * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  strcpy(result, str1);
  strcat(result, str2);
  return result;
}

char *copy_pystring(PyObject *pystr) {
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

char *copy_pybytes(PyObject *pybytes) {
  Py_ssize_t og_size = 0;
  char *og_str;
  if (PyBytes_AsStringAndSize(pybytes, &og_str, &og_size) < 0) {
    return NULL;
  }
  size_t new_str_len = og_size + 1;
  char *result = malloc(new_str_len * sizeof(char));
  if (result == NULL) {
    return NULL;
  }
  strcpy(result, og_str);
  return result;
}

MapKeyVal *MapKeyVal_new(size_t count) {
  MapKeyVal *new_map = (MapKeyVal *)malloc(sizeof(MapKeyVal));
  new_map->count = count;
  new_map->keys = malloc(sizeof(char *) * count);
  new_map->values = malloc(sizeof(char *) * count);
  return new_map;
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
                        const char *venv_path) {
  WsgiApp *app = malloc(sizeof(WsgiApp));
  if (app == NULL) {
    return NULL;
  }
  PyGILState_STATE gstate = PyGILState_Ensure();

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
                            MapKeyVal *headers, const char *body) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *environ = PyDict_New();
  for (size_t i = 0; i < headers->count; i++) {
    PyObject *key = PyUnicode_FromString(headers->keys[i]);
    PyObject *value = PyUnicode_FromString(headers->values[i]);
    PyDict_SetItem(environ, key, value);
    Py_DECREF(key);
    Py_DECREF(value);
  }
  PyObject *input_key = PyUnicode_FromString("wsgi.input");
  PyObject *bytes = PyBytes_FromString(body);
  PyObject *bytes_file = PyObject_CallOneArg(BytesIO, bytes);
  PyDict_SetItem(environ, input_key, bytes_file);
  Py_DECREF(input_key);
  Py_DECREF(bytes);
  Py_DECREF(bytes_file);

  char *extra_keys[] = {"wsgi.multithread", "wsgi.multiprocess",
                        "wsgi.run_once", "wsgi.version", "wsgi.errors"};
  PyObject *extra_values[] = {Py_True, Py_True, Py_False, wsgi_version,
                              sys_stderr};
  for (size_t i = 0; i < 5; i++) {
    PyObject *key = PyUnicode_FromString(extra_keys[i]);
    PyDict_SetItem(environ, key, extra_values[i]);
    Py_DECREF(key);
  }
  RequestResponse *r =
      (RequestResponse *)PyObject_CallObject((PyObject *)&ResponseType, NULL);
  r->app = app;
  r->request_id = request_id;
  r->request_environ = environ;
  PyObject_CallOneArg(task_queue_put, (PyObject *)r);

  PyGILState_Release(gstate);
}

static void MapKeyVal_free(MapKeyVal *map, size_t pos) {
  if (pos > map->count) {
    pos = map->count;
  }
  for (size_t i = 0; i < pos; i++) {
    free(map->keys[i]);
    free(map->values[i]);
  }
  free(map);
}

static PyObject *response_callback(PyObject *self, PyObject *args) {
  RequestResponse *response = (RequestResponse *)PyTuple_GetItem(args, 0);
  PyObject *exc_info = PyTuple_GetItem(args, 1);
  if (exc_info != Py_None) {
    PyErr_Display(NULL, exc_info, NULL);
    goto finalize_error;
  }

  char *response_body = NULL;
  if (response->response_body) {
    PyObject *iterator = PyObject_GetIter(response->response_body);
    if (iterator) {
      PyObject *item;
      while ((item = PyIter_Next(iterator))) {
        if (!PyBytes_Check(item)) {
          PyErr_SetString(PyExc_RuntimeError,
                          "expected response body items to be bytes");
          PyErr_Print();
          Py_DECREF(item);
          Py_DECREF(iterator);
          if (response_body != NULL) {
            free(response_body);
          }
          goto finalize_error;
        }
        char *previous_body = response_body;
        if (previous_body == NULL) {
          response_body = concatenate_strings("", PyBytes_AsString(item));
        } else {
          response_body =
              concatenate_strings(previous_body, PyBytes_AsString(item));
          free(previous_body);
        }
        Py_DECREF(item);
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
  size_t pos = 0;
  while ((item = PyIter_Next(iterator))) {
    if (!PyTuple_Check(item) || PyTuple_Size(item) != 2) {
      PyErr_SetString(PyExc_RuntimeError,
                      "expected response headers to be tuples with 2 items");
      PyErr_Print();
      Py_DECREF(item);
      Py_DECREF(iterator);
      MapKeyVal_free(http_headers, pos);
      goto finalize_error;
    }
    key = PyTuple_GetItem(item, 0);
    value = PyTuple_GetItem(item, 1);
    http_headers->keys[pos] = copy_pystring(key);
    http_headers->values[pos] = copy_pystring(value);
    Py_DECREF(item);
    pos++;
  }
  Py_DECREF(iterator);

  Py_BEGIN_ALLOW_THREADS wsgi_write_response(response->request_id,
                                             response->response_status,
                                             http_headers, response_body);
  Py_END_ALLOW_THREADS goto end;

finalize_error:
  Py_BEGIN_ALLOW_THREADS wsgi_write_response(response->request_id, 500, NULL,
                                             NULL);
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
};

AsgiApp *AsgiApp_import(const char *module_name, const char *app_name,
                        const char *venv_path) {
  AsgiApp *app = malloc(sizeof(AsgiApp));
  if (app == NULL) {
    return NULL;
  }
  PyGILState_STATE gstate = PyGILState_Ensure();

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

struct AsgiEvent {
  PyObject_HEAD AsgiApp *app;
  uint64_t request_id;
  PyObject *event_ts;
  PyObject *request_body;
  PyObject *scope_dict;
};

static PyObject *AsgiEvent_new(PyTypeObject *type, PyObject *args,
                               PyObject *kwds) {
  AsgiEvent *self;
  self = (AsgiEvent *)type->tp_alloc(type, 0);
  if (self != NULL) {
    self->request_id = 0;
    self->event_ts = NULL;
    self->request_body = NULL;
    self->scope_dict = NULL;
  }
  return (PyObject *)self;
}

static void AsgiEvent_dealloc(AsgiEvent *self) {
  Py_XDECREF(self->event_ts);
  Py_XDECREF(self->request_body);
  Py_XDECREF(self->scope_dict);
  Py_TYPE(self)->tp_free((PyObject *)self);
}

void AsgiEvent_set(AsgiEvent *self, const char *body) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  if (body) {
    self->request_body = PyBytes_FromString(body);
  }
  PyObject *set_fn = PyObject_GetAttrString((PyObject *)self->event_ts, "set");
  PyObject_CallNoArgs(set_fn);
  Py_DECREF(set_fn);
  PyGILState_Release(gstate);
}

static PyObject *AsgiEvent_wait(AsgiEvent *self, PyObject *args) {
  PyObject *wait_fn =
      PyObject_GetAttrString((PyObject *)self->event_ts, "wait");
  PyObject *coro = PyObject_CallNoArgs(wait_fn);
  Py_DECREF(wait_fn);
  return coro;
}

static PyObject *AsgiEvent_clear(AsgiEvent *self, PyObject *args) {
  PyObject *clear_fn =
      PyObject_GetAttrString((PyObject *)self->event_ts, "clear");
  PyObject_CallNoArgs(clear_fn);
  Py_DECREF(clear_fn);
  Py_RETURN_NONE;
}

static PyObject *AsgiEvent_receive_start(AsgiEvent *self, PyObject *args) {
  asgi_receive_start(self->request_id, self);
  Py_RETURN_NONE;
}

static PyObject *AsgiEvent_receive_end(AsgiEvent *self, PyObject *args) {
  PyObject *data = PyDict_New();
  PyObject *data_type = PyUnicode_FromString("http.request");
  PyDict_SetItemString(data, "type", data_type);
  PyDict_SetItemString(data, "body", self->request_body);
  PyDict_SetItemString(data, "more_body", Py_False);
  Py_DECREF(data_type);
  Py_DECREF(self->request_body);
  return data;
}

static PyObject *AsgiEvent_send(AsgiEvent *self, PyObject *args) {
  PyObject *data = PyTuple_GetItem(args, 0);
  PyObject *data_type = PyDict_GetItemString(data, "type");
  if (PyUnicode_CompareWithASCIIString(data_type, "http.response.start") == 0) {
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
    size_t pos = 0;
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
      http_headers->keys[pos] = copy_pybytes(key);
      http_headers->values[pos] = copy_pybytes(value);
      Py_DECREF(item);
      pos++;
    }
    Py_DECREF(iterator);

    asgi_set_headers(self->request_id, PyLong_AsLong(status_code), http_headers,
                     self);
  } else if (PyUnicode_CompareWithASCIIString(data_type,
                                              "http.response.body") == 0) {
    PyObject *more_body = PyDict_GetItemString(data, "more_body");
    uint8_t send_more_body = 1;
    if (!more_body ||
        PyObject_RichCompareBool(more_body, Py_False, Py_EQ) == 1) {
      send_more_body = 0;
    }
    PyObject *body = PyDict_GetItemString(data, "body");
    asgi_send_response(self->request_id, PyBytes_AsString(body), send_more_body,
                       self);
  }
  Py_RETURN_NONE;
}

static PyMethodDef AsgiEvent_methods[] = {
    {"wait", (PyCFunction)AsgiEvent_wait, METH_VARARGS,
     "Wait until ASGI Event is set, calls the underlying asnycio.Event set() "
     "method."},
    {"clear", (PyCFunction)AsgiEvent_clear, METH_VARARGS,
     "Clear ASGI Event, calls the underlying asnycio.Event clear() method."},
    {"receive_start", (PyCFunction)AsgiEvent_receive_start, METH_VARARGS,
     "Start reading receive data."},
    {"receive_end", (PyCFunction)AsgiEvent_receive_end, METH_VARARGS,
     "Return all received data."},
    {"send", (PyCFunction)AsgiEvent_send, METH_VARARGS,
     "Send data back to client."},
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
                            int server_port) {
  PyGILState_STATE gstate = PyGILState_Ensure();

  PyObject *scope_dict = PyDict_New();
  PyDict_SetItemString(scope_dict, "asgi", asgi_version);

  for (int i = 0; i < scope->count; i++) {
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

  PyObject *headers_tuple = PyTuple_New(headers->count);
  for (int i = 0; i < headers->count; i++) {
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

  AsgiEvent *asgi_event =
      (AsgiEvent *)PyObject_CallObject((PyObject *)&AsgiEventType, NULL);
  asgi_event->app = app;
  asgi_event->request_id = request_id;
  asgi_event->event_ts = PyObject_CallNoArgs(asyncio_Event_ts);
  asgi_event->scope_dict = scope_dict;

  PyObject *receive =
      PyObject_CallOneArg(build_receive, (PyObject *)asgi_event);
  PyObject *send = PyObject_CallOneArg(build_send, (PyObject *)asgi_event);

  PyObject *args = PyTuple_New(3);
  PyTuple_SetItem(args, 0, scope_dict);
  PyTuple_SetItem(args, 1, receive);
  PyTuple_SetItem(args, 2, send);
  PyObject *coro = PyObject_Call((PyObject *)app->handler, args, NULL);
  Py_DECREF(args);

  args = PyTuple_New(2);
  PyTuple_SetItem(args, 0, coro);
  PyTuple_SetItem(args, 1, asyncio_Loop);
  PyObject_Call(asyncio_run_coroutine_threadsafe, args, NULL);
  Py_DECREF(args);

  Py_DECREF(asgi_event);

  PyGILState_Release(gstate);
}

void AsgiApp_cleanup(AsgiApp *app) {
  PyGILState_STATE gstate = PyGILState_Ensure();
  Py_XDECREF(app->handler);
  PyGILState_Release(gstate);
  free(app);
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
