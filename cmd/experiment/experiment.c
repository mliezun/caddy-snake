#include <Python.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <unistd.h>

PyObject *write_bytes;
PyObject *send_bytes;
PyObject *send_bytes2;
PyObject *send_bytes3;
PyObject *send_bytes4;

static void Debug_obj(PyObject *obj) {
  PyObject *repr = PyObject_Repr(obj);
  printf("%s\n", PyUnicode_AsUTF8(repr));
  Py_DECREF(repr);
}

void send_message(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes, py_msg);
  Py_DECREF(py_msg);
}

void send_message2(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes2, py_msg);
  Py_DECREF(py_msg);
}

void send_message3(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes3, py_msg);
  Py_DECREF(py_msg);
}

void send_message4(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(send_bytes4, py_msg);
  Py_DECREF(py_msg);
}

void send_message_v2(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject_CallOneArg(write_bytes, py_msg);
  Py_DECREF(py_msg);
}

void Py_init_experiment(const char *setup_py) {
  PyStatus status;
  PyConfig config;
  PyConfig_InitPythonConfig(&config);
  status = PyConfig_SetString(&config, &config.program_name, L"experiment");
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

  send_bytes = PyObject_GetAttrString(PyTuple_GetItem(create_process_result, 0),
                                      "send_bytes");
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

  return;
exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}

// Grows the buffer to at least double its current size.
// *buffer: pointer to current buffer pointer
// *current_size: pointer to current buffer size
// Returns 0 on success, -1 on failure
int grow_buffer(char **buffer, size_t *current_size) {
  size_t new_size = (*current_size == 0) ? 1 : (*current_size * 2);

  char *new_buf;
  if (*buffer == NULL) {
    // allocate initially
    new_buf = malloc(new_size);
  } else {
    // realloc to bigger buffer
    new_buf = realloc(*buffer, new_size);
  }

  if (!new_buf) {
    // allocation failed
    return -1;
  }

  *buffer = new_buf;
  *current_size = new_size;
  return 0;
}

void execute_worker_v2(const char *setup_py, int fd[2]) {
  PyStatus status;
  PyConfig config;
  PyConfig_InitPythonConfig(&config);
  // Set the program name. Implicitly preinitialize Python
  status = PyConfig_SetString(&config, &config.program_name, L"experiment");
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
  write_bytes = PyObject_GetAttrString(main_module, "write_bytes");

  while (1) {

    // Using this technique of a growing buffer is faster, but ends
    // up reading more than expected from each request, which is not
    // correct.
    size_t buffer_size = 8192;
    char *buffer = malloc(buffer_size);
    ssize_t total = 0;
    do {
      if (total == buffer_size) {
        grow_buffer(&buffer, &buffer_size);
      }
      size_t n = read(fd[0], buffer + total, buffer_size - total);
      total += n;
    } while (total == buffer_size);
    if (total == 0) {
      free(buffer);
      break;
    }
    send_message_v2(buffer, total);
    free(buffer);
  }

  return;
exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}

int fd[2];
int fda[2];
int fdb[2];
int fdc[2];

void Py_init_experiment_v2(const char *setup_py) {
  pid_t pid;

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

    execute_worker_v2(setup_py, fd);

    exit(EXIT_SUCCESS);
  } else {
    // 👨‍👧 Parent process
    close(fd[0]); // Close read end

    // Create pipe
    if (pipe(fda) == -1) {
      perror("pipe");
      exit(EXIT_FAILURE);
    }

    pid = fork();
    if (pid < 0) {
      perror("fork");
      exit(EXIT_FAILURE);
    }

    if (pid == 0) {
      close(fda[1]); // Close write end

      execute_worker_v2(setup_py, fda);

      exit(EXIT_SUCCESS);
    } else {
      close(fda[0]); // Close read end

      // Create pipe
      if (pipe(fdb) == -1) {
        perror("pipe");
        exit(EXIT_FAILURE);
      }

      pid = fork();
      if (pid < 0) {
        perror("fork");
        exit(EXIT_FAILURE);
      }

      if (pid == 0) {
        close(fdb[1]); // Close write end

        execute_worker_v2(setup_py, fdb);

        exit(EXIT_SUCCESS);
      } else {
        close(fdb[0]); // Close read end

        // Create pipe
        if (pipe(fdc) == -1) {
          perror("pipe");
          exit(EXIT_FAILURE);
        }

        pid = fork();
        if (pid < 0) {
          perror("fork");
          exit(EXIT_FAILURE);
        }

        if (pid == 0) {
          close(fdc[1]); // Close write end

          execute_worker_v2(setup_py, fdc);

          exit(EXIT_SUCCESS);
        } else {
          close(fdc[0]); // Close read end
        }
      }
    }
  }
}

void go_send_message(char *msg, size_t msg_size, int ix) {
  switch (ix % 4) {
  case 0:
    write(fd[1], msg, msg_size);
    break;
  case 1:
    write(fda[1], msg, msg_size);
    break;
  case 2:
    write(fdb[1], msg, msg_size);
    break;
  case 3:
    write(fdc[1], msg, msg_size);
    break;
  }
}

void execute_worker_v3(const char *setup_py) {
  PyStatus status;
  PyConfig config;
  PyConfig_InitPythonConfig(&config);
  // Set the program name. Implicitly preinitialize Python
  status = PyConfig_SetString(&config, &config.program_name, L"experiment");
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
  write_bytes = PyObject_GetAttrString(main_module, "write_bytes");

  return;
exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}

void Py_init_experiment_v3(const char *setup_py) {
  
}