export default function Modal({ modal, onConfirm, onCancel }) {
  if (!modal) return null
  const { title, message, confirmLabel = 'OK', cancelLabel = null, danger = false } = modal
  return (
    <div className="modal-overlay" onClick={cancelLabel ? onCancel : undefined}>
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <div className={'modal-title' + (danger ? ' danger' : '')}>{title}</div>
        <div className="modal-body">{message}</div>
        <div className="modal-actions">
          {cancelLabel && (
            <button className="btn btn-secondary" onClick={onCancel}>Batal</button>
          )}
          <button
            className={danger ? 'btn btn-danger' : 'btn btn-primary'}
            onClick={onConfirm}
            autoFocus
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
