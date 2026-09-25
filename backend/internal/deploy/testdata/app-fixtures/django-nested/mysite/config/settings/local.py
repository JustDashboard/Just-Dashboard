from .base import *  # noqa: F403

import debug_toolbar  # noqa: F401  (only in requirements/local.txt)

DEBUG = True
SECRET_KEY = "django-insecure-local"
ALLOWED_HOSTS = ["*"]
