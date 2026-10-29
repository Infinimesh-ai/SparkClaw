import { useState } from 'react';

export function PreviewWarnings({ warnings, zh }: { warnings?: string[]; zh: boolean }) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed || !warnings?.length) return null;
  return <aside className="previewWarnings" role="note" aria-label={zh ? '字体提示' : 'Font notice'}>
    <details><summary>{zh ? '部分字体不可用，预览版式可能与原件不同。' : 'Some fonts are unavailable. Preview layout may differ from the original.'}</summary>
      {warnings.map((warning, index) => <p key={index}>{warning}</p>)}
      <p>{zh ? '如需原字体效果，请安装相应系统字体后重新打开文档。' : 'For the original font appearance, install the required system fonts and reopen this document.'}</p>
    </details>
    <button type="button" onClick={() => setDismissed(true)} aria-label={zh ? '关闭字体提示' : 'Dismiss font notice'}>×</button>
  </aside>;
}
