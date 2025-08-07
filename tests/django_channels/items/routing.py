from django.urls import re_path
from . import consumers

websocket_urlpatterns = [
    re_path(r"ws/items/$", consumers.ItemConsumer.as_asgi()),
]
