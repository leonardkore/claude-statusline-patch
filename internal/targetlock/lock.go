package targetlock

import "errors"

var ErrBusy = errors.New("ensure lock busy")
