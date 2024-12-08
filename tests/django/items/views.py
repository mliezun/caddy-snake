import json

from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt


db = {}

@csrf_exempt
def store_item(request, item_id: str):
    content = request.body.decode("utf-8")
    db[item_id] = json.loads(content)
    return JsonResponse("Stored", safe=False)

@csrf_exempt
def retrieve_item(request, item_id: str):
    return JsonResponse(db.get(item_id), safe=False)

@csrf_exempt
def delete_item(request, item_id: str):
    del db[item_id]
    return JsonResponse("Deleted", safe=False)

