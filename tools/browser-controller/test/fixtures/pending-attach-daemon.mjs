// Inert process fixture: never opens a browser or network connection.
process.send?.('ready');
setInterval(()=>{},1000);
