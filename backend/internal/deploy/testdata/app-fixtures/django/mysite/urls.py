from django.contrib.auth.models import User
from django.http import HttpResponse
from django.urls import path


def index(request):
    # Reads the migrated table so a start without migrations would fail here.
    count = User.objects.count()
    return HttpResponse(f"<h1>https://django.build-value.test users={count}</h1>")


urlpatterns = [path("", index)]
