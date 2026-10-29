import { useEffect, useMemo, useState } from 'react';
import { Workbook } from '@fortune-sheet/react';
import '@fortune-sheet/react/dist/index.css';
import { DocumentChannel, type SpreadsheetModel } from './channel';
import { allowsReadonlyKey, checkedSpreadsheet, fortuneSheet, literalCell, selectedClipboard } from './spreadsheetModel';
export default function SpreadsheetPreview({channel}:{channel:DocumentChannel}) {
  const zh=channel.document.language==='zh';const [model,setModel]=useState<SpreadsheetModel>();const [error,setError]=useState('');const [sheetId,setSheetId]=useState(channel.document.position?.sheet||'');const [selection,setSelection]=useState({row:[0,0],column:[0,0]});
  useEffect(()=>{let active=true;void channel.request('model').then(value=>{const parsed=checkedSpreadsheet(value);if(active){setModel(parsed);setSheetId(current=>parsed.sheets.some(s=>s.id===current)?current:parsed.sheets[0].id);}}).catch((cause:Error)=>{if(active)setError(cause.message);});return()=>{active=false;};},[channel]);
  const sheet=model?.sheets.find(s=>s.id===sheetId);const data=useMemo(()=>sheet?[fortuneSheet(sheet)]:[],[sheet]);
  const selected=sheet?.cells.find(c=>c.row===selection.row[0]&&c.column===selection.column[0]);
  const hooks=useMemo(()=>({beforeUpdateCell:()=>false,beforePaste:()=>false,beforeUpdateComment:()=>false,beforeUpdateSheetName:()=>false,afterSelectionChange:(_id:string,value:{row:number[];column:number[]})=>{if(value?.row?.length>=1&&value?.column?.length>=1)setSelection({row:[value.row[0],value.row[1]??value.row[0]],column:[value.column[0],value.column[1]??value.column[0]]});}}),[]);
  if(error)return <p className="previewMessage" role="alert">{error}</p>;
  if(!sheet)return <p className="previewMessage">{zh?'正在读取表格…':'Reading spreadsheet…'}</p>;
  return <div className="spreadsheetPreview"><div className="previewToolbar sheetTabs" role="tablist">{model!.sheets.map(s=><button type="button" role="tab" aria-selected={s.id===sheetId} key={s.id} onClick={()=>{setSheetId(s.id);setSelection({row:[0,0],column:[0,0]});channel.position({sheet:s.id});}}>{s.name}</button>)}</div>
    {!!model!.warnings.length&&<details className="sheetWarnings"><summary>{zh?'部分内容无法完整预览':'Some workbook features cannot be previewed'}</summary>{model!.warnings.map((warning,index)=><p key={index}>{warning}</p>)}</details>}
    <div className="literalFormula" aria-label={zh?'单元格内容':'Cell content'}><span>{selection.row[0]+1}:{selection.column[0]+1}</span><output>{selected?.formula?`=${selected.formula.replace(/^=/u,'')}`:selected?literalCell(selected):''}</output>{selected?.formula&&<span>{selected.missingCachedValue?(zh?'没有保存的计算结果':'Saved result unavailable'):`${zh?'保存值':'Saved'}: ${literalCell(selected)}`}</span>}</div>
    <div className="readonlySheet" onKeyDownCapture={event=>{if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='c'){event.stopPropagation();return;}if(!allowsReadonlyKey(event.nativeEvent)){event.preventDefault();event.stopPropagation();}}} onBeforeInputCapture={event=>{event.preventDefault();event.stopPropagation();}} onPasteCapture={event=>{event.preventDefault();event.stopPropagation();}} onCutCapture={event=>{event.preventDefault();event.stopPropagation();}} onDropCapture={event=>{event.preventDefault();event.stopPropagation();}} onDragStartCapture={event=>{event.preventDefault();event.stopPropagation();}} onDoubleClickCapture={event=>{event.preventDefault();event.stopPropagation();}} onContextMenu={event=>{event.preventDefault();event.stopPropagation();}} onCopyCapture={event=>{event.preventDefault();event.stopPropagation();try{const value=selectedClipboard(sheet,selection);event.clipboardData.setData('text/plain',value.text);event.clipboardData.setData('text/html',value.html);}catch(cause){setError((cause as Error).message);}}}>
      <Workbook key={sheet.id} data={data} allowEdit={false} forceCalculation={false} showToolbar={false} showFormulaBar={false} showSheetTabs={false} addRows={0} row={sheet.rowCount} column={sheet.columnCount} devicePixelRatio={1} lang={zh?'zh':'en'} cellContextMenu={[]} headerContextMenu={[]} sheetTabContextMenu={[]} filterContextMenu={[]} hooks={hooks} onChange={changed => {
        const rows=changed[0]?.data;if(!rows)return;
        for(const cell of sheet.cells){const value=rows[cell.row]?.[cell.column];if(String(value?.v??'')!==literalCell(cell)||value?.f){setError(zh?'已阻止表格内容变更。':'A worksheet mutation was blocked.');return;}}
      }} />
    </div></div>;
}
