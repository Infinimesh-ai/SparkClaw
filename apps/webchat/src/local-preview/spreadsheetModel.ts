import type { SpreadsheetCell, SpreadsheetModel } from './channel';
export function checkedSpreadsheet(value: unknown): SpreadsheetModel {
  const model=value as SpreadsheetModel;
  if(model?.kind!=='spreadsheet'||!Array.isArray(model.sheets)||model.sheets.length<1||model.sheets.length>32||!Array.isArray(model.warnings)||model.warnings.length>100)throw new Error('Invalid spreadsheet model');
  let area=0,cells=0,characters=0;
  for(const sheet of model.sheets){
    if(typeof sheet.id!=='string'||sheet.id.length>128||typeof sheet.name!=='string'||sheet.name.length>1024||!Number.isSafeInteger(sheet.rowCount)||!Number.isSafeInteger(sheet.columnCount)||sheet.rowCount<1||sheet.columnCount<1||!Array.isArray(sheet.cells)||!Array.isArray(sheet.merges))throw new Error('Invalid worksheet');
    area+=sheet.rowCount*sheet.columnCount;cells+=sheet.cells.length;if(area>100_000||cells>50_000||sheet.merges.length>10_000)throw new Error('Spreadsheet exceeds the cell limit');
    for(const cell of sheet.cells){if(!Number.isSafeInteger(cell.row)||!Number.isSafeInteger(cell.column)||cell.row<0||cell.column<0||cell.row>=sheet.rowCount||cell.column>=sheet.columnCount||(!['string','number','boolean'].includes(typeof cell.value)&&cell.value!==null)||typeof cell.value==='number'&&!Number.isFinite(cell.value)||cell.formula!==undefined&&typeof cell.formula!=='string')throw new Error('Invalid cell');characters+=String(cell.value??'').length+(cell.formula?.length||0);}
    for(const merge of sheet.merges)if(![merge.row,merge.column,merge.rowSpan,merge.columnSpan].every(Number.isSafeInteger)||merge.row<0||merge.column<0||merge.rowSpan<1||merge.columnSpan<1||merge.row+merge.rowSpan>sheet.rowCount||merge.column+merge.columnSpan>sheet.columnCount)throw new Error('Invalid merge');
  }
  if(characters>4*1024*1024)throw new Error('Spreadsheet exceeds the text limit');
  if(model.warnings.some(w=>typeof w!=='string'||w.length>4096))throw new Error('Invalid worksheet warning');
  return model;
}
export function literalCell(cell: SpreadsheetCell) { return cell.missingCachedValue ? '' : String(cell.value ?? ''); }
export function escapeHTML(value: string) { return value.replace(/[&<>"']/gu,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!)); }
export function selectedClipboard(sheet: SpreadsheetModel['sheets'][number], selection: {row:number[];column:number[]}) {
  const r1=selection.row[0],r2=selection.row[1]??r1,c1=selection.column[0],c2=selection.column[1]??c1;
  if(![r1,r2,c1,c2].every(Number.isSafeInteger)||r1<0||c1<0||r2<r1||c2<c1||r2>=sheet.rowCount||c2>=sheet.columnCount||(r2-r1+1)*(c2-c1+1)>10_000)throw new Error('Copy selection exceeds the cell limit');
  const cells=new Map(sheet.cells.map(cell=>[`${cell.row}:${cell.column}`,cell])); const rows:string[][]=[];let bytes=0;
  for(let row=r1;row<=r2;row++){const values=[];for(let col=c1;col<=c2;col++){const cell=cells.get(`${row}:${col}`);const raw=cell?literalCell(cell):'';const value=cell&&typeof cell.value==='string'&&/^\s*[=+\-@]/u.test(raw)?"'"+raw:raw;bytes+=value.length;if(bytes>1024*1024)throw new Error('Copy selection exceeds the text limit');values.push(value);}rows.push(values);}
  return {text:rows.map(row=>row.map(value=>/[\t\n"]/u.test(value)?`"${value.replace(/"/gu,'""')}"`:value).join('\t')).join('\n'),html:`<table>${rows.map(row=>`<tr>${row.map(value=>`<td style="mso-number-format:'\\@'">${escapeHTML(value)}</td>`).join('')}</tr>`).join('')}</table>`};
}
export function fortuneSheet(sheet: SpreadsheetModel['sheets'][number]) {
  const merges=Object.fromEntries(sheet.merges.map(m=>[`${m.row}_${m.column}`,{r:m.row,c:m.column,rs:m.rowSpan,cs:m.columnSpan}]));
  const color=(value:unknown)=>typeof value==='string'&&/^#[0-9a-f]{6}$/iu.test(value)?value:undefined;
  const cells = new Map(sheet.cells.map(cell=>[`${cell.row}:${cell.column}`,{r:cell.row,c:cell.column,v:{v:literalCell(cell),m:literalCell(cell),ct:{fa:'@',t:'s'},bl:cell.style?.bold?1:0,it:cell.style?.italic?1:0,fc:color(cell.style?.color),bg:color(cell.style?.background),fs:Math.min(36,Math.max(8,Number(cell.style?.fontSize)||12)),ht:cell.style?.align==='right'?2:cell.style?.align==='center'?0:1,mc:undefined as undefined|{r:number;c:number;rs?:number;cs?:number}}}]));
  for(const merge of sheet.merges)for(let row=merge.row;row<merge.row+merge.rowSpan;row++)for(let col=merge.column;col<merge.column+merge.columnSpan;col++){
    const key=`${row}:${col}`;const item=cells.get(key)||{r:row,c:col,v:{v:'',m:'',ct:{fa:'@',t:'s'},bl:0,it:0,fc:undefined,bg:undefined,fs:12,ht:1,mc:undefined}};
    item.v.mc=row===merge.row&&col===merge.column?{r:row,c:col,rs:merge.rowSpan,cs:merge.columnSpan}:{r:merge.row,c:merge.column};cells.set(key,item);
  }
  return {id:sheet.id,name:sheet.name,row:sheet.rowCount,column:sheet.columnCount,status:1,order:0,config:{merge:merges},celldata:[...cells.values()]};
}
export function allowsReadonlyKey(event: Pick<KeyboardEvent,'key'|'ctrlKey'|'metaKey'|'altKey'>) {
  return !event.altKey && ((event.ctrlKey||event.metaKey) ? ['c','a'].includes(event.key.toLowerCase()) : ['ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Home','End','PageUp','PageDown','Tab','Escape','Shift','Control','Meta'].includes(event.key));
}
