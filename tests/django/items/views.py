from django.http import JsonResponse


db = {}


def store_item(request, item_id: str):
    content = request.get_json()
    db[id] = content
    return JsonResponse("Stored", safe=False)

def retrieve_item(request, item_id: str):
    return JsonResponse(db.get(id), safe=False)

def delete_item(request, item_id: str):
    del db[id]
    return JsonResponse("Deleted", safe=False)

