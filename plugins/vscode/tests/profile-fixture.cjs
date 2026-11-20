'use strict';
// Disposable real extensions for the publisher probe; never shipped/installed
// by the product. Commands exercise VS Code's public settings/VSIX APIs.
const fs=require('node:fs'),path=require('node:path'),{crc32}=require('node:zlib');
function zip(files){
 const chunks=[],directory=[];let offset=0;
 for(const [name,value]of Object.entries(files)){
  const filename=Buffer.from(name),body=Buffer.from(value),crc=crc32(body),header=Buffer.alloc(30),entry=Buffer.alloc(46);
  header.writeUInt32LE(0x04034b50);header.writeUInt16LE(20,4);header.writeUInt32LE(crc,14);header.writeUInt32LE(body.length,18);header.writeUInt32LE(body.length,22);header.writeUInt16LE(filename.length,26);
  entry.writeUInt32LE(0x02014b50);entry.writeUInt16LE(20,4);entry.writeUInt16LE(20,6);entry.writeUInt32LE(crc,16);entry.writeUInt32LE(body.length,20);entry.writeUInt32LE(body.length,24);entry.writeUInt16LE(filename.length,28);entry.writeUInt32LE(offset,42);
  chunks.push(header,filename,body);directory.push(entry,filename);offset+=header.length+filename.length+body.length;
 }
 const central=Buffer.concat(directory),end=Buffer.alloc(22);end.writeUInt32LE(0x06054b50);end.writeUInt16LE(directory.length/2,8);end.writeUInt16LE(directory.length/2,10);end.writeUInt32LE(central.length,12);end.writeUInt32LE(offset,16);
 return Buffer.concat([...chunks,central,end]);
}
function setup(root,env,editor,localProduct,secrets=false){
 const profile=path.join(env.OMO_HOME,'plugins','.data','vscode','profiles',editor);
 let sharedUser=path.join(profile,'preferences','User'),extensions=path.join(profile,'extensions');
 if(localProduct){
  const home=process.platform==='win32'?env.USERPROFILE:env.HOME;
  const config=process.platform==='darwin'?path.join(home,'Library','Application Support'):process.platform==='win32'?env.APPDATA:env.XDG_CONFIG_HOME;
  sharedUser=path.join(config,localProduct,'User');extensions=path.join(home,localProduct==='Code'?'.vscode':'.vscode-oss','extensions');
  fs.mkdirSync(sharedUser,{recursive:true});fs.writeFileSync(path.join(sharedUser,'settings.json'),'// Desktop fixture preferences\n{"editor.fontSize":19,"workbench.colorTheme":"Default Light Modern","chat.disableAIFeatures":true,}\n');
 }
 if(secrets){
  if(!localProduct||process.platform!=='linux')throw new Error('The encrypted secret probe requires a Linux desktop fixture');
  const {DatabaseSync}=require('node:sqlite'),{createCipheriv,pbkdf2Sync}=require('node:crypto');
  fs.mkdirSync(path.join(sharedUser,'globalStorage'),{recursive:true});
  const db=new DatabaseSync(path.join(sharedUser,'globalStorage','state.vscdb'));db.exec('CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB)');
  for(const [extensionId,key,value]of [
   ['vscode.github-authentication','github.auth',JSON.stringify([{id:'omo-fixture-session',account:{id:'123456',label:'omo-secret-fixture'},scopes:['repo'],accessToken:'synthetic-probe-token'}])],
   ['omo.profile-probe','fixture','synthetic-extension-secret'],
  ]){
   const cipher=createCipheriv('aes-128-cbc',pbkdf2Sync('peanuts','saltysalt',1,16,'sha1'),Buffer.alloc(16,32));
   db.prepare('INSERT INTO ItemTable VALUES (?,?)').run('secret://'+JSON.stringify({extensionId,key}),JSON.stringify(Buffer.concat([Buffer.from('v10'),cipher.update(value),cipher.final()])));
  }
  db.close();
 }
 const vsix=path.join(root,'browser-installed.vsix');
 const installedManifest={name:'browser-installed',publisher:'omo',version:'1.0.0',engines:{vscode:'^1.109.0'},main:'./extension.cjs',extensionKind:['workspace'],activationEvents:['*']};
 const manifestXML='<?xml version="1.0"?><PackageManifest Version="2.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011"><Metadata><Identity Language="en-US" Id="browser-installed" Version="1.0.0" Publisher="omo"/><DisplayName>omo browser-installed fixture</DisplayName><Description xml:space="preserve">Disposable probe</Description></Metadata><Installation><InstallationTarget Id="Microsoft.VisualStudio.Code"/></Installation><Dependencies/><Assets><Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" Addressable="true"/></Assets></PackageManifest>';
 fs.writeFileSync(vsix,zip({'extension/package.json':JSON.stringify(installedManifest),'extension/extension.cjs':`exports.activate=()=>{const v=require('vscode'),fs=require('node:fs'),p=require('node:path');fs.writeFileSync(p.join(v.workspace.workspaceFolders[0].uri.fsPath,'.omo-installed-active'),'active')};`,
  'extension.vsixmanifest':manifestXML,'[Content_Types].xml':'<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="json" ContentType="application/json"/><Default Extension="cjs" ContentType="application/javascript"/><Default Extension="vsixmanifest" ContentType="text/xml"/></Types>'}));
 const folder=path.join(extensions,'omo.profile-probe-1.0.0');fs.mkdirSync(folder,{recursive:true});
 fs.writeFileSync(path.join(folder,'package.json'),JSON.stringify({name:'profile-probe',publisher:'omo',version:'1.0.0',engines:{vscode:'^1.109.0'},main:'./extension.cjs',extensionKind:['workspace'],activationEvents:['*'],contributes:{commands:[{command:'omo.fixture.inspect',title:'omo fixture: Inspect preferences'},{command:'omo.fixture.secrets',title:'omo fixture: Inspect sign-ins'},{command:'omo.fixture.setFont',title:'omo fixture: Set font size'},{command:'omo.fixture.install',title:'omo fixture: Install extension'},{command:'omo.fixture.preference',title:'omo fixture: Preference file'}]}}));
 fs.writeFileSync(path.join(folder,'extension.cjs'),`const v=require('vscode'),fs=require('node:fs'),p=require('node:path');exports.activate=async context=>{
 const folder=v.workspace.workspaceFolders[0].uri.fsPath,instance=require('node:crypto').randomUUID();let sequence=0;
 context.subscriptions.push(v.commands.registerCommand('omo.fixture.secrets',async()=>{
  const githubAccounts=await v.authentication.getAccounts('github');
  const extensionSecretMatches=(await context.secrets.get('fixture'))==='synthetic-extension-secret';
  const file=p.join(folder,'.omo-fixture-signins.json');fs.writeFileSync(file+'.next',JSON.stringify({instance,githubAccounts,extensionSecretMatches}));fs.renameSync(file+'.next',file);
 }));
 context.subscriptions.push(v.commands.registerCommand('omo.fixture.inspect',()=>{const file=p.join(folder,'.omo-fixture-settings.json'),value=JSON.stringify({instance,sequence:++sequence,activeUri:v.window.activeTextEditor?.document.uri.toString(),fontInspection:v.workspace.getConfiguration('editor').inspect('fontSize'),font:v.workspace.getConfiguration('editor').get('fontSize'),theme:v.workspace.getConfiguration('workbench').get('colorTheme'),chatDisabled:v.workspace.getConfiguration('chat').get('disableAIFeatures'),chatKnown:typeof v.workspace.getConfiguration('chat').inspect('disableAIFeatures')?.defaultValue==='boolean'});fs.writeFileSync(file+'.next',value);fs.renameSync(file+'.next',file);}));
 context.subscriptions.push(v.commands.registerCommand('omo.fixture.setFont',()=>v.workspace.getConfiguration('editor').update('fontSize',22,v.ConfigurationTarget.Global)));
 context.subscriptions.push(v.commands.registerCommand('omo.fixture.install',()=>v.commands.executeCommand('workbench.extensions.installExtension',v.Uri.file(${JSON.stringify(vsix)}))));
 context.subscriptions.push(v.commands.registerCommand('omo.fixture.preference',async()=>{
  const request=JSON.parse(fs.readFileSync(p.join(folder,'.omo-preference-request.json'),'utf8')),response={id:request.id};
  const root=${editor==='code-server'?'v.Uri.file('+JSON.stringify(path.join(profile,'portable','user-data','User'))+').with({scheme:"vscode-remote"})':'v.Uri.from({scheme:"vscode-userdata",path:"/User"})'},uri=v.Uri.joinPath(root,...request.name.split('/'));
  try{if(request.action==='write'){await v.workspace.fs.createDirectory(v.Uri.joinPath(uri,'..'));await v.workspace.fs.writeFile(uri,Buffer.from(request.text));}else{try{response.text=Buffer.from(await v.workspace.fs.readFile(uri)).toString('utf8');}catch(error){if(error.code==='FileNotFound'||error.code==='ENOENT')response.text=null;else throw error;}}}catch(error){response.error=String(error.code||error.message);}
  const target=p.join(folder,'.omo-preference-response.json');fs.writeFileSync(target+'.next',JSON.stringify(response));fs.renameSync(target+'.next',target);
 }));
 await v.commands.getCommands(true);fs.writeFileSync(p.join(folder,'.omo-fixture-active'),'active');
 };`);
 return {sharedUser,extensions,localProduct};
}
module.exports={setup};
