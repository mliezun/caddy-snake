#include <Python.h>
#include <stdlib.h>

PyObject *send_bytes;
PyObject *send_bytes2;
PyObject *send_bytes3;
PyObject *send_bytes4;

static void Debug_obj(PyObject *obj) {
  PyObject *repr = PyObject_Repr(obj);
  printf("%s\n", PyUnicode_AsUTF8(repr));
  Py_DECREF(repr);
}

void Py_init_experiment(const char *setup_py) {
  int fd[2];
  pid_t pid;
  char buffer[8192];

  // Create pipe
  if (pipe(fd) == -1) {
    perror("pipe");
    exit(EXIT_FAILURE);
  }

  pid = fork();
  if (pid < 0) {
    perror("fork");
    exit(EXIT_FAILURE);
  }

  if (pid == 0) {
    // 👶 Child process
    close(fd[1]); // Close write end

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

    PyRun_SimpleString(setup_py);
    PyObject *main_module = PyImport_AddModule("__main__");
    PyObject *create_process_fn =
        PyObject_GetAttrString(main_module, "create_process");

    PyObject *create_process_result = PyObject_CallNoArgs(create_process_fn);

    send_bytes = PyObject_GetAttrString(
        PyTuple_GetItem(create_process_result, 0), "send_bytes");
    send_bytes2 = PyObject_GetAttrString(
        PyTuple_GetItem(create_process_result, 1), "send_bytes");
    send_bytes3 = PyObject_GetAttrString(
        PyTuple_GetItem(create_process_result, 2), "send_bytes");
    send_bytes4 = PyObject_GetAttrString(
        PyTuple_GetItem(create_process_result, 3), "send_bytes");
    Debug_obj(send_bytes);
    Debug_obj(send_bytes2);
    Debug_obj(send_bytes3);
    Debug_obj(send_bytes4);

    while (1) {
      ssize_t n = read(fd[0], buffer, sizeof(buffer) - 1);
      if (n >= 0) {
        buffer[n] = '\0'; // Null-terminate
        printf("child received: %s\n", buffer);
      } else {
        perror("read");
      }
    }

    exit(EXIT_SUCCESS);
    // exception:
    //   PyConfig_Clear(&config);
    //   Py_ExitStatusException(status);
  } else {
    // 👨‍👧 Parent process
    close(fd[0]); // Close read end

    const char *msg = "Hello from parent";
    write(fd[1], msg, strlen(msg));
    // close(fd[1]); // Done writing
  }
}

void send_message(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes, py_msg);
  Py_DECREF(py_msg);
}

void send_message2(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes, py_msg);
  Py_DECREF(py_msg);
}

void send_message3(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes, py_msg);
  Py_DECREF(py_msg);
}

void send_message4(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes, py_msg);
  Py_DECREF(py_msg);
}
