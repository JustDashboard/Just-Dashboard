require "sinatra/base"

class App < Sinatra::Base
  get "/" do
    "<h1>https://sinatra.build-value.test</h1>"
  end
end
