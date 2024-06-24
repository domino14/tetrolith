import lustre_websocket as ws

pub type Msg {
  WsWrapper(ws.WebSocketEvent)
}

fn init() {
  #(Model(None), ws.init("/ws", WsWrapper))
}
