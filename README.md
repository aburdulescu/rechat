# rechat

Very simple demo chat app using Websockets and Redis PubSub

## Build

```
git clone https://github.com/aburdulescu/rechat.git
cd rechat
go build
```

## Usage

- install and then start Redis:

For example, on Debian:
```
sudo apt install redis
sudo systemctl start redis-server.service
```

- start `rechat` server:

```
cd rechat
./rechat
```

- open the address "http://localhost:8080" in a couple of browser tabs,
write messages in each and you should see them propagate to each tab

- the chat is scalable, meaning that you can start 2(or more) `rechat` instances and 
connect different clients to each and you will still be able to communicate
between the clients connected to the two different instances
