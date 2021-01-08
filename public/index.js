function connect() {
    let ws = new WebSocket("ws://"+window.location.host+"/ws/343677762813eaeb65704cc8d9e96f7a444ba0cca92ff861af7f68648b3e6ef1");
    let heartbeatID = "";
    ws.onmessage = (e)=>{
        const data = JSON.parse(e.data);
        console.log(data);
        if (data.event === "reloadpage") {
            window.location.reload();
        }
    };
    ws.onclose = ()=>{
        setTimeout(connect,1000);
        clearInterval(heartbeatID);
    };
    ws.onopen = ()=>{
        heartbeatID = setInterval(() => {
            if (ws.readyState == WebSocket.OPEN) {
                ws.send(":heartbeat");
            }
        }, 10000);
    };
}
