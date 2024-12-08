from django.urls import path

from . import views

urlpatterns = [
    path("store/<uuid:item_id>", views.store_item, name="store_item"),
    path("retrieve/<uuid:item_id>", views.retrieve_item, name="retrieve_item"),
    path("delete/<uuid:item_id>", views.delete_item, name="delete_item"),
]
