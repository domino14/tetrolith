import gleam/option.{type Option, None, Some}
import gleam/result
import gleam/uri
import lustre
import lustre/effect
import lustre_websocket as ws
import modem

pub type Model {
  NotInRoom(uri: uri.Uri)
  InRoom(uri: uri.Uri, active_game: Option(ActiveGame))
}

pub type RoomCode =
  String

pub type PlayerName =
  String

pub type ActiveGame {
  ActiveGame(ws: ws.WebSocket)
}

pub type Route {
  Home
  Play(room_code: Option(String))
  NotFound
}

pub type GameSession {
  GameSession(
    session_id: RoomCode,
    players: List(PlayerName),
    word_list_name: String,
    // more; the status of the boards.
  )
}

pub type WebsocketResponse {
  // Sent after connecting to a room.
  InitialGameState(GameSession)
  //   PlayersInRoom(List(PlayerName))
  //   WordList(List(String))
  //   RoundInfo(Round)
  //   RoundResult(FinishedRound)
  ServerError(reason: String)
}

pub type Msg {
  OnRouteChange(uri.Uri, Route)
  WebSocketEvent(ws.WebSocketEvent)
  OnWebsocketMessage(WebsocketResponse)

  JoinGame

  StartRound
}

pub fn main() {
  let app = lustre.application(init, update, view)
  let assert Ok(_) = lustre.start(app, "#app", Nil)

  Nil
}

fn get_route_from_uri(uri: uri.Uri) -> Route {
  let room_code =
    uri.query
    |> option.map(uri.parse_query)
    |> option.then(fn(query) {
      case query {
        Ok([#("game", room_code)]) -> Some(room_code)
        _ -> None
      }
    })
  case uri.path_segments(uri.path), room_code {
    [""], _ | [], _ -> Home
    ["play"], room_code -> Play(room_code)
    _, _ -> NotFound
  }
}

fn init(_flags) -> #(Model, effect.Effect(Msg)) {
  let uri = modem.initial_uri()
  case uri, uri |> result.map(get_route_from_uri) {
    Ok(uri), Ok(Play(Some(room_code))) -> {
      let rejoin =
        storage.local()
        |> result.try(fn(local_storage) {
          use id <- result.try(storage.get_item(local_storage, "connection_id"))
          use name <- result.try(storage.get_item(local_storage, "player_name"))
          use stored_room_code <- result.try(storage.get_item(
            local_storage,
            "room_code",
          ))
          let uri.Uri(_, _, host, port, _, _, _) = uri
          let host = option.unwrap(host, "localhost")
          let port =
            option.map(port, fn(port) { ":" <> int.to_string(port) })
            |> option.unwrap("")
          case room_code == stored_room_code {
            True ->
              Ok(#(
                id,
                name,
                ws.init(
                  "wss://" <> host <> port <> "/ws/" <> id <> "/" <> name,
                  WebSocketEvent,
                ),
              ))
            False -> {
              storage.clear(local_storage)
              Error(Nil)
            }
          }
        })
      case rejoin {
        Ok(#(id, name, msg)) -> #(
          InRoom(uri, id, room_code, name, None, DisplayState(Round, False)),
          msg,
        )
        Error(_) -> #(
          NotInRoom(
            uri,
            Play(Some(room_code)),
            room_code,
            Some("Sorry, please try joining again."),
          ),
          effect.batch([join_game(uri, room_code), modem.init(on_url_change)]),
        )
      }
    }
    Ok(uri), Ok(route) -> #(
      NotInRoom(uri, route, "", None),
      modem.init(on_url_change),
    )
    Error(Nil), _ | _, Error(Nil) -> #(
      NotInRoom(relative(""), Home, "", None),
      modem.init(on_url_change),
    )
  }
}

fn on_url_change(uri: uri.Uri) -> Msg {
  get_route_from_uri(uri) |> OnRouteChange(uri, _)
}
// pub type Msg {
//   WsWrapper(ws.WebSocketEvent)
// }

// type State {
//   State(ws: WsWrapper, ct: int)
// }

// fn init() {
//   #(State(None), ws.init("/ws", WsWrapper))
// }

// pub fn main() {
//   let app = lustre.simple(init, update, view)
// }

// fn update(model, msg) {
//   case msg {
//     WsWrapper(InvalidUrl) -> panic
//     WsWrapper(OnOpen(socket)) -> #(
//       State(..model, ws: Some(socket)),
//       ws.send(socket, "client-init"),
//     )
//     WsWrapper(OnTextMessage(msg)) -> todo
//     WsWrapper(OnBinaryMessage(msg)) -> todo as "either-or"
//     WsWrapper(OnClose(reason)) -> #(State(..model, ws: None), effect.none())
//   }
// }

// fn view(model: State) {
//   let count = int.to_string(model.ct)

//   div([], [
//     button([on_click(Incr)], [text(" + ")]),
//     p([], [text(count)]),
//     button([on_click(Decr)], [text(" - ")]),
//   ])
// }
