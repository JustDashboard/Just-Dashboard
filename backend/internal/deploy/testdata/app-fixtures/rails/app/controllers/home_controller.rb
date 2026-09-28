class HomeController < ApplicationController
  def index
    render html: helpers.safe_join([
      helpers.tag.h1("https://rails.build-value.test notes=#{Note.count}"),
      helpers.javascript_include_tag("value")
    ])
  end
end
