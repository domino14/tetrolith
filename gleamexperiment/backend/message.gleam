pub type Message {
  Connect(Player)
  Disconnect(Player)
  FindGame(Player, String)
  StartGame(Int, String)
}

pub type Player {
  Player(String)
}

pub type Move {
  SolveAnagram(String)
}
