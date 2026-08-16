export default function Modal({ modal, onConfirm, onCancel }) {
  if (!modal) return null
  const { title, message, confirmLabel = 'OK', cancelLabel = null, danger = false } = modal
  return (
    <div className="modal-overlay" onClick={cancelLabel ? onCancel : undefined}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h3 style={{ color: danger ? 'var(--danger)' : undefined }}>{title}</h3>
          {cancelLabel && (
            <button className="icon-btn" onClick={onCancel} aria-label="Tutup">
              <span className="ms">close</span>
            </button>
          )}
        </div>
        <div className="modal-body">{message}</div>
        <div className="modal-foot">
          {cancelLabel && (
            <button className="btn btn-secondary" onClick={onCancel}>{cancelLabel}</button>
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