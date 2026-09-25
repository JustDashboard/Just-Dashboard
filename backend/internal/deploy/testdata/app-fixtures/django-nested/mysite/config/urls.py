import psycopg2
from django.contrib.auth.models import User
from django.http import HttpResponse
from django.urls import path


def index(request):
    # Reads the migrated table and the compiled driver, so a start without
    # migrations or without libpq would fail here.
    return HttpResponse(f"<h1>https://django-nested.build-value.test users={User.objects.count()} psycopg2={psycopg2.__version__.split()[0]}</h1>")


urlpatterns = [path("", index)]
