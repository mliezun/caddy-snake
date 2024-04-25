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

static PyObject *wsgi_version;
static PyObject *sys_stderr;
static PyObject *BytesIO;
static PyObject *task_queue_put;

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

char *copy_string(PyObject *pystr) {
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

HTTPHeaders *HTTPHeaders_new(size_t count) {
  HTTPHeaders *new_request = (HTTPHeaders *)malloc(sizeof(HTTPHeaders));
  new_request->count = count;
  new_request->keys = malloc(sizeof(char *) * count);
  new_request->values = malloc(sizeof(char *) * count);
  return new_request;
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
                            HTTPHeaders *headers, const char *body) {
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

static void HTTPHeaders_free(HTTPHeaders *http_headers, size_t pos) {
  if (pos > http_headers->count) {
    pos = http_headers->count;
  }
  for (size_t i = 0; i < pos; i++) {
    free(http_headers->keys[i]);
    free(http_headers->values[i]);
  }
  free(http_headers);
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

  HTTPHeaders *http_headers = HTTPHeaders_new(headers_count);

  PyObject *key, *value, *item;
  size_t pos = 0;
  while ((item = PyIter_Next(iterator))) {
    if (!PyTuple_Check(item) || PyTuple_Size(item) != 2) {
      PyErr_SetString(PyExc_RuntimeError,
                      "expected response headers to be tuples with 2 items");
      PyErr_Print();
      Py_DECREF(item);
      Py_DECREF(iterator);
      HTTPHeaders_free(http_headers, pos);
      goto finalize_error;
    }
    key = PyTuple_GetItem(item, 0);
    value = PyTuple_GetItem(item, 1);
    http_headers->keys[pos] = copy_string(key);
    http_headers->values[pos] = copy_string(value);
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

void Py_init_and_release_gil() {
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

  PyObject *caddysnake_module = PyModule_Create(&CaddysnakeModule);
  PyObject *response_callback_fn =
      PyObject_GetAttrString(caddysnake_module, "response_callback");

  // Initialize types
  PyType_Ready(&ResponseType);

  // Setup task queue and consumer threads
  PyRun_SimpleString(
      "def _setup_caddysnake(callback):\n"
      "\tfrom queue import SimpleQueue\n"
      "\tfrom threading import Thread\n"
      "\ttask_queue = SimpleQueue()\n"
      "\tdef process_request_response(task):\n"
      "\t\ttry:\n"
      "\t\t\tresult = task.call_wsgi()\n"
      "\t\t\tcallback(task, None)\n"
      "\t\texcept Exception as e:\n"
      "\t\t\tcallback(task, e)\n"
      "\tdef worker():\n"
      "\t\twhile True:\n"
      "\t\t\ttask = task_queue.get()\n"
      "\t\t\tThread(target=process_request_response, args=(task,)).start()\n"
      "\tThread(target=worker).start()\n"
      "\treturn task_queue\n");
  PyObject *main_module = PyImport_AddModule("__main__");
  PyObject *setup_fn = PyObject_GetAttrString(main_module, "_setup_caddysnake");
  PyObject *task_queue = PyObject_CallOneArg(setup_fn, response_callback_fn);
  task_queue_put = PyObject_GetAttrString(task_queue, "put");
  PyRun_SimpleString("del _setup_caddysnake");

  // Setup WSGI version
  wsgi_version = PyTuple_New(2);
  PyTuple_SetItem(wsgi_version, 0, PyLong_FromLong(1));
  PyTuple_SetItem(wsgi_version, 1, PyLong_FromLong(0));

  // Setup stderr for logging
  sys_stderr = PySys_GetObject("stderr");

  // This are global objects expected to exist during the entire program
  // lifetime. Refcounts can be safely decreased, but there's no need to do it
  // because we expect the objects to stick around forever.
  // Py_DECREF(task_queue);
  // Py_DECREF(setup_fn);
  // Py_DECREF(response_callback_fn);
  // Py_DECREF(io_module);
  // Py_DECREF(caddysnake_module);

  PyEval_ReleaseThread(PyGILState_GetThisThreadState());
  return;

exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}

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
/*
# Standard application
async def application(scope, receive, send):
  event = await receive()
  ...
  await send({"type": "websocket.send", ...})

# Server implementation
def build_receive(request_id):
  async def receive():
    while True:
      # asgi_* functions are in Go
      if asgi_event_ready(request_id):
        return asgi_receive_event(request_id)
      # Release control to event loop
      await asyncio.sleep(0)
  return receive

def build_send(request_id):
  async def send(event):
    # asgi_* functions are in Go
    asgi_send_event(request_id, event)
    while True:
      if asgi_event_sent(request_id):
        return
      # Release control to event loop
      await asyncio.sleep(0)

# Alternative implementation
def build_receive(event):
  async def receive():
    event.asgi_receive_data_start()
    await event.wait()
    return event.asgi_receive_data_end()
  return receive

def build_send(event):
  async def send(data):
    event.asgi_send_data(data)
    await event.wait()

PyObject *asgi_receive_data_start(PyObject *self) {
  go_receive_data_start(self->request_id, (void *) self); // Call go
}

func go_receive_data_start(request_id C.int64_t, object unsafe.Pointer) {
  go func() {
    lock.Lock()
    hn := asgi_handlers[int(request_id)]
    lock.Unlock()
    data := io.ReadAll(hn.Body)
    hn.Data = C.CString(unsafe.Pointer(data))
    asgi_set_event(object, hn.Data)
  }()
}

void asgi_set_event(void *object, char *data) {
  // Acquire GIL
  PyObject *event = (PyObject *) object;
  event->data = data;
  PyObject *event_set = PyObject_GetAttrString(event, "set");
  PyObject_Call(event_set, NULL, NULL);
  // Release GIL
}

PyObject *asgi_receive_data_start(PyObject *self) {
  return PyBytes_FromString(self->data);
}
*/
