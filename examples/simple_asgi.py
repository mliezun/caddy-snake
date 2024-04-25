async def main(scope, receive, send):
    print("Inside ASGI!", scope, receive, send)
    event = receive()
    print("Event to wait", event)
    data = await event
    print("got",data)
    await send(data)
