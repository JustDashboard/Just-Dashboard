package controllers

import javax.inject.*
import play.api.mvc.*

@Singleton
class HomeController @Inject() (val controllerComponents: ControllerComponents) extends BaseController {
  def index() = Action {
    Ok("<h1>https://play.build-value.test</h1>").as(HTML)
  }
}
