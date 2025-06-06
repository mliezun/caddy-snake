def caddysnake_setup_wsgi(callback):
    from queue import SimpleQueue
    from threading import Thread

    task_queue = SimpleQueue()

    def process_request_response(task):
        try:
            task.call_wsgi()
            callback(task, None)
        except Exception as e:
            callback(task, e)

    def worker():
        while True:
            task = task_queue.get()
            Thread(target=process_request_response, args=(task,)).start()

    Thread(target=worker).start()

    return task_queue


def caddysnake_setup_asgi(loop):
    import asyncio
    from threading import Thread

    # See: https://stackoverflow.com/questions/33000200/asyncio-wait-for-event-from-other-thread
    class Event_ts(asyncio.Event):
        def set(self):
            loop.call_soon_threadsafe(super().set)

    def build_receive(asgi_event):
        async def receive():
            ev = asgi_event.receive_start()
            if ev:
                await ev.wait()
                ev.clear()
                result = asgi_event.receive_end()
                return result
            else:
                return {"type": "http.disconnect"}

        return receive

    def build_send(asgi_event):
        async def send(data):
            ev = asgi_event.send(data)
            await ev.wait()
            ev.clear()

        return send

    def build_lifespan(app, state):
        import sys

        scope = {
            "type": "lifespan",
            "asgi": {
                "version": "3.0",
                "spec_version": "2.3",
            },
            "state": state,
        }

        startup_ok = asyncio.Future(loop=loop)
        shutdown_ok = asyncio.Future(loop=loop)

        async def send(data):
            if data.get("message") and data["type"].endswith("failed"):
                print(data["message"], file=sys.stderr)

            ok = data["type"].endswith(".complete")
            if "startup" in data["type"]:
                startup_ok.set_result(ok)
            if "shutdown" in data["type"]:
                shutdown_ok.set_result(ok)

        receive_queue = asyncio.Queue()

        async def receive():
            return await receive_queue.get()

        def wrap_future(future):
            async def wrapper():
                return await future

            return wrapper()

        def lifespan_startup():
            loop.call_soon_threadsafe(
                receive_queue.put_nowait, {"type": "lifespan.startup"}
            )
            coro = wrap_future(startup_ok)
            fut = asyncio.run_coroutine_threadsafe(coro, loop=loop)
            return fut.result()

        def lifespan_shutdown():
            loop.call_soon_threadsafe(
                receive_queue.put_nowait, {"type": "lifespan.shutdown"}
            )
            coro = wrap_future(shutdown_ok)
            fut = asyncio.run_coroutine_threadsafe(coro, loop=loop)
            return fut.result()

        def run_lifespan():
            coro = app(scope, receive, send)
            fut = asyncio.run_coroutine_threadsafe(coro, loop)
            fut.result()

        Thread(target=run_lifespan).start()

        return lifespan_startup, lifespan_shutdown

    Thread(target=loop.run_forever).start()

    class WebsocketClosed(IOError):
        pass

    return (
        Event_ts,
        build_receive,
        build_send,
        build_lifespan,
        WebsocketClosed,
    )
