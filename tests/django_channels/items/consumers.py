import json
import sys
from channels.generic.websocket import AsyncWebsocketConsumer


# In-memory database for testing (similar to the original Django test)
db = {}


class ItemConsumer(AsyncWebsocketConsumer):
    async def connect(self):
        print(f"WebSocket connected: {self.channel_name}", file=sys.stderr)
        await self.accept()

    async def disconnect(self, close_code):
        print(f"WebSocket disconnected: {self.channel_name}", file=sys.stderr)

    async def receive(self, text_data):
        try:
            data = json.loads(text_data)
            action = data.get("action")
            item_id = data.get("item_id")

            if action == "store":
                item_data = data.get("item")
                db[item_id] = item_data
                await self.send(
                    text_data=json.dumps(
                        {
                            "action": "store_response",
                            "item_id": item_id,
                            "status": "success",
                            "message": "Stored",
                        }
                    )
                )

            elif action == "retrieve":
                item_data = db.get(item_id)
                await self.send(
                    text_data=json.dumps(
                        {
                            "action": "retrieve_response",
                            "item_id": item_id,
                            "status": "success" if item_data is not None else "error",
                            "item": item_data,
                        }
                    )
                )

            elif action == "delete":
                if item_id in db:
                    del db[item_id]
                    await self.send(
                        text_data=json.dumps(
                            {
                                "action": "delete_response",
                                "item_id": item_id,
                                "status": "success",
                                "message": "Deleted",
                            }
                        )
                    )
                else:
                    await self.send(
                        text_data=json.dumps(
                            {
                                "action": "delete_response",
                                "item_id": item_id,
                                "status": "error",
                                "message": "Item not found",
                            }
                        )
                    )

            elif action == "ping":
                # Echo back the ping with item data
                await self.send(
                    text_data=json.dumps({"action": "pong", "data": data.get("data")})
                )

            else:
                await self.send(
                    text_data=json.dumps(
                        {"action": "error", "message": f"Unknown action: {action}"}
                    )
                )

        except json.JSONDecodeError:
            await self.send(
                text_data=json.dumps({"action": "error", "message": "Invalid JSON"})
            )
        except Exception as e:
            await self.send(
                text_data=json.dumps({"action": "error", "message": str(e)})
            )
