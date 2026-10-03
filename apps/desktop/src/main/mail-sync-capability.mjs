const CHANNEL='sparkclaw-mail-sync:invoke';
// The renderer supplies only an operation and mailbox ID. Authentication,
// installation identity, transport and cache scope stay in the main process.
export class MailSyncCapability {
  constructor({ipcMain,window,client}){Object.assign(this,{ipcMain,window,client});}
  start(){this.ipcMain.handle(CHANNEL,(event,request)=>this.dispatch(event,request));return this;}
  close(){this.ipcMain.removeHandler(CHANNEL);}
  async dispatch(event,request){
    let url;try{url=new URL(event.senderFrame?.url);}catch{}
    if(event.sender!==this.window.webContents||event.senderFrame!==this.window.webContents.mainFrame||!url||url.protocol!=='sparkclaw-app:'||url.hostname!=='workbench'||url.username||url.password)throw new Error('Mail sync sender is not trusted');
    if(!request||typeof request!=='object'||Array.isArray(request)||request.schema_version!==1)throw new Error('Invalid mail sync request');
    const args=['read','sync'].includes(request.operation)?['mailbox_id']:[];
    if(Object.keys(request).sort().join(',')!==['schema_version','operation',...args].sort().join(','))throw new Error('Invalid mail sync request fields');
    switch(request.operation){case 'catalog':return this.client.catalog();case 'refreshCatalog':return this.client.refreshCatalog();case 'read':return this.client.read(request.mailbox_id);case 'sync':return this.client.sync(request.mailbox_id);default:throw new Error('Mail sync operation is unavailable');}
  }
}
