import sys
import multiprocessing
import multiprocessing.connection

multiprocessing.allow_connection_pickling()


def subtask(pipe):
    logs_file = f"logs_{pipe._handle}.txt"
    with open(logs_file, "a") as f:
        f.write(f"{pipe._handle}\n")
    for _ in range(1_000_000):
        with open(logs_file, "a") as f:
            f.write(f"{len(pipe.recv_bytes())=}\n")


def create_process():
    sys.stderr.write("starting process\n")
    pipe_read, pipe_write = multiprocessing.connection.Pipe(duplex=False)
    proc = multiprocessing.get_context("fork").Process(
        target=subtask, name="subtask", args=(pipe_read,)
    )
    proc.start()
    pipe_read2, pipe_write2 = multiprocessing.connection.Pipe(duplex=False)
    proc2 = multiprocessing.get_context("fork").Process(
        target=subtask, name="subtask", args=(pipe_read2,)
    )
    proc2.start()
    pipe_read3, pipe_write3 = multiprocessing.connection.Pipe(duplex=False)
    proc3 = multiprocessing.get_context("fork").Process(
        target=subtask, name="subtask", args=(pipe_read3,)
    )
    proc3.start()
    pipe_read4, pipe_write4 = multiprocessing.connection.Pipe(duplex=False)
    proc4 = multiprocessing.get_context("fork").Process(
        target=subtask, name="subtask", args=(pipe_read4,)
    )
    proc4.start()
    return pipe_write, pipe_write2, pipe_write3, pipe_write4
