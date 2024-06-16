// import gleam/io
import gleam/bytes_builder
import gleam/dynamic
import gleam/erlang/os
import gleam/erlang/process
import gleam/http/request.{type Request, get_query}
import gleam/http/response.{type Response}
import gleam/io
import gleam/list
import gleam/option.{None, Some}
import gleam/otp/actor
import gleam/result
import gwt.{type Jwt, type JwtDecodeError, type Verified}

import mist.{type Connection, type ResponseData}

pub fn main() {
  let not_found =
    response.new(404)
    |> response.set_body(mist.Bytes(bytes_builder.new()))

  let assert Ok(secret_key) = os.get_env("SECRET_KEY")

  let assert Ok(_) =
    fn(req: Request(Connection)) -> Response(ResponseData) {
      case request.path_segments(req) {
        ["ws"] -> connect_websocket(req, secret_key)
        ["echo"] -> echo_body(req)

        _ -> not_found
      }
    }
    |> mist.new
    |> mist.port(3000)
    |> mist.start_http

  process.sleep_forever()
}

pub type MyMessage {
  Broadcast(String)
}

fn connect_websocket(
  req: Request(Connection),
  secret_key: String,
) -> Response(ResponseData) {
  let selector = process.new_selector()
  let jwt = extract_jwt(req, secret_key)

  case jwt {
    Ok(token) ->
      // Process the JWT token if needed, then initialize WebSocket
      case gwt.get_payload_claim(token, "usn", dynamic.string) {
        Ok(username) ->
          mist.websocket(
            request: req,
            on_init: fn(_conn) {
              io.println(username <> " connected")
              #(username, Some(selector))
            },
            on_close: fn(_state) { io.println("goodbye " <> username) },
            handler: handle_ws_message,
          )

        Error(_) ->
          response.new(400)
          |> response.set_body(
            mist.Bytes(bytes_builder.from_string("Invalid JWT - no usn found")),
          )
          |> response.set_header("content-type", "text/plain")
      }

    Error(reason) ->
      response.new(400)
      |> response.set_body(mist.Bytes(bytes_builder.from_string(reason)))
      |> response.set_header("content-type", "text/plain")
  }
}

fn extract_jwt(
  req: Request(Connection),
  secret_key: String,
) -> Result(Jwt(Verified), String) {
  case get_query(req) {
    Ok(params) ->
      case list.key_find(params, "jwt") {
        Ok(token) ->
          case gwt.from_signed_string(token, secret_key) {
            Ok(token) -> Ok(token)
            Error(_) -> Error("JWT decode error")
          }

        Error(_) -> Error("JWT key not found")
      }

    Error(_) -> Error("Failed to parse query string")
  }
}

fn handle_ws_message(state, conn, message) {
  case message {
    mist.Text("ping") -> {
      let assert Ok(_) = mist.send_text_frame(conn, "pong " <> state)
      actor.continue(state)
    }
    mist.Text(_) | mist.Binary(_) -> {
      actor.continue(state)
    }
    mist.Custom(Broadcast(text)) -> {
      let assert Ok(_) = mist.send_text_frame(conn, text)
      actor.continue(state)
    }
    mist.Closed | mist.Shutdown -> actor.Stop(process.Normal)
  }
}

fn echo_body(request: Request(Connection)) -> Response(ResponseData) {
  let content_type =
    request
    |> request.get_header("content-type")
    |> result.unwrap("text/plain")

  mist.read_body(request, 1024 * 1024 * 10)
  |> result.map(fn(req) {
    response.new(200)
    |> response.set_body(mist.Bytes(bytes_builder.from_bit_array(req.body)))
    |> response.set_header("content-type", content_type)
  })
  |> result.lazy_unwrap(fn() {
    response.new(400)
    |> response.set_body(mist.Bytes(bytes_builder.new()))
  })
}
