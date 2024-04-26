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
            Thread(
                target=process_request_response,
                args=(task,)
            ).start()

    Thread(target=worker).start()
    
    return task_queue

def caddysnake_setup_asgi(loop):
    import asyncio
    from threading import Thread
    
    # See: https://stackoverflow.com/questions/33000200/asyncio-wait-for-event-from-other-thread
    class Event_ts(asyncio.Event):
        def set(self):
            loop.call_soon_threadsafe(super().set)
            
        def clear(self):
            loop.call_soon_threadsafe(super().clear)
            
    def build_receive(asgi_event):
        async def receive():
            asgi_event.receive_start()
            await asgi_event.wait()
            asgi_event.clear()
            return asgi_event.receive_end()
        return receive
    
    def build_send(asgi_event):
        async def send(data):
            asgi_event.send(data)
            await asgi_event.wait()
            asgi_event.clear()
        return send
    
    Thread(target=loop.run_forever).start()
            
    return Event_ts, build_receive, build_send
