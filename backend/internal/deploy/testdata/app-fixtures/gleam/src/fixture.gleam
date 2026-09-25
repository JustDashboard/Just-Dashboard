import envoy
import gleam/bytes_tree
import gleam/erlang/process
import gleam/http/response
import gleam/int
import gleam/result
import mist

pub fn main() {
  let port =
    envoy.get("PORT")
    |> result.try(int.parse)
    |> result.unwrap(8000)
  let assert Ok(_) =
    fn(_request) {
      response.new(200)
      |> response.set_header("content-type", "text/html")
      |> response.set_body(
        mist.Bytes(bytes_tree.from_string("<h1>https://gleam.build-value.test</h1>")),
      )
    }
    |> mist.new
    |> mist.bind("0.0.0.0")
    |> mist.port(port)
    |> mist.start
  process.sleep_forever()
}
