import { Login, Logout, SelectFile, Upload } from './wailsjs/go/main/App.js'
import { EventsOn } from './wailsjs/runtime/runtime.js'

const loginForm = document.getElementById('loginForm')
const form = document.getElementById('uploadForm')
const loginButton = document.getElementById('loginButton')
const logoutButton = document.getElementById('logoutButton')
const selectFileButton = document.getElementById('selectFile')
const startUploadButton = document.getElementById('startUpload')
const clearLogButton = document.getElementById('clearLog')
const fileNameElement = document.getElementById('fileName')
const filePathElement = document.getElementById('filePath')
const authStatusElement = document.getElementById('authStatus')
const statusElement = document.getElementById('status')
const progressBarElement = document.getElementById('progressBar')
const progressTextElement = document.getElementById('progressText')
const logElement = document.getElementById('log')
const uploadInputs = [
  document.getElementById('remotePath'),
  document.getElementById('concurrency'),
]

let selectedFilePath = ''
let loggedIn = false

setLoggedIn(false)

loginForm.addEventListener('submit', async (event) => {
  event.preventDefault()

  loginButton.disabled = true
  authStatusElement.textContent = 'Logging in'
  writeLog('Logging in')

  try {
    const result = await Login({
      serverUrl: document.getElementById('serverUrl').value,
      username: document.getElementById('username').value,
      password: document.getElementById('password').value,
      skipTlsVerify: document.getElementById('skipTlsVerify').checked,
    })
    document.getElementById('password').value = ''
    setLoggedIn(true)
    authStatusElement.textContent = `Logged in as ${result.username}`
    writeLog(`Logged in to ${result.serverUrl} as ${result.username}`)
  } catch (error) {
    setLoggedIn(false)
    authStatusElement.textContent = 'Login failed'
    writeLog(`Login failed: ${formatError(error)}`)
  } finally {
    loginButton.disabled = loggedIn
  }
})

logoutButton.addEventListener('click', () => {
  Logout()
  handleLoggedOut('Logged out')
})

selectFileButton.addEventListener('click', async () => {
  if (!loggedIn) {
    writeLog('Login before selecting a file')
    return
  }

  try {
    const filePath = await SelectFile()
    if (!filePath) {
      return
    }
    selectedFilePath = filePath
    fileNameElement.textContent = filePath.split(/[\\/]/).pop()
    filePathElement.textContent = filePath
    writeLog(`Selected ${filePath}`)
  } catch (error) {
    writeLog(`File selection failed: ${formatError(error)}`)
  }
})

form.addEventListener('submit', async (event) => {
  event.preventDefault()

  const request = {
    remotePath: document.getElementById('remotePath').value,
    filePath: selectedFilePath,
    concurrency: Number(document.getElementById('concurrency').value),
  }

  setBusy(true)
  setProgress(0, 'Starting upload')
  writeLog('Starting upload')

  try {
    const result = await Upload(request)
    setProgress(100, `Uploaded ${result.fileName}`)
    writeLog(`Upload complete: ${result.fileName} (${prettyFileSize(result.size)}) in ${result.elapsed}`)
  } catch (error) {
    statusElement.textContent = 'Failed'
    const message = formatError(error)
    writeLog(`Upload failed: ${message}`)
    if (message.toLowerCase().includes('session expired') || message.toLowerCase().includes('login')) {
      handleLoggedOut('Session expired. Please login again.')
    }
  } finally {
    setBusy(false)
  }
})

clearLogButton.addEventListener('click', () => {
  logElement.textContent = ''
})

EventsOn('upload:progress', (event) => {
  const message = `${event.percent.toFixed(1)}% - ${event.completedChunks}/${event.totalChunks} chunks - ${prettyFileSize(event.completedBytes)} / ${prettyFileSize(event.totalBytes)}`
  setProgress(event.percent, message)
})

EventsOn('upload:log', (message) => {
  writeLog(message)
})

EventsOn('auth:expired', () => {
  handleLoggedOut('Session expired. Please login again.')
})

function setBusy(isBusy) {
  startUploadButton.disabled = isBusy
  selectFileButton.disabled = isBusy
  statusElement.textContent = isBusy ? 'Uploading' : (loggedIn ? 'Idle' : 'Login required')
  if (!isBusy) {
    setLoggedIn(loggedIn)
  }
}

function setLoggedIn(isLoggedIn) {
  loggedIn = isLoggedIn
  loginButton.disabled = isLoggedIn
  logoutButton.disabled = !isLoggedIn
  selectFileButton.disabled = !isLoggedIn
  startUploadButton.disabled = !isLoggedIn
  document.getElementById('serverUrl').disabled = isLoggedIn
  document.getElementById('username').disabled = isLoggedIn
  document.getElementById('password').disabled = isLoggedIn
  document.getElementById('skipTlsVerify').disabled = isLoggedIn
  uploadInputs.forEach((input) => {
    input.disabled = !isLoggedIn
  })
  form.classList.toggle('locked', !isLoggedIn)
  if (!isLoggedIn) {
    authStatusElement.textContent = 'Not logged in'
  }
}

function handleLoggedOut(message) {
  const alreadyLoggedOut = !loggedIn
  selectedFilePath = ''
  fileNameElement.textContent = 'No file selected'
  filePathElement.textContent = ''
  setProgress(0, '0%')
  setLoggedIn(false)
  authStatusElement.textContent = message
  statusElement.textContent = 'Login required'
  if (!alreadyLoggedOut) {
    writeLog(message)
  }
}

function setProgress(percent, message) {
  const safePercent = Math.max(0, Math.min(100, percent || 0))
  progressBarElement.style.transform = `scaleX(${safePercent / 100})`
  progressTextElement.textContent = message
}

function writeLog(message) {
  const now = new Date().toLocaleTimeString()
  logElement.textContent += `[${now}] ${message}\n`
  logElement.scrollTop = logElement.scrollHeight
}

function formatError(error) {
  if (error == null) {
    return 'unknown error'
  }
  if (typeof error === 'string') {
    return error
  }
  return error.message || JSON.stringify(error)
}

function prettyFileSize(fileSizeInBytes) {
  if (fileSizeInBytes == null) {
    return 'unknown size'
  }
  if (fileSizeInBytes < 1024) {
    return `${fileSizeInBytes} B`
  }
  if (fileSizeInBytes < 1024 * 1024) {
    return `${(fileSizeInBytes / 1024).toFixed(2)} KiB`
  }
  if (fileSizeInBytes < 1024 * 1024 * 1024) {
    return `${(fileSizeInBytes / (1024 * 1024)).toFixed(2)} MiB`
  }
  return `${(fileSizeInBytes / (1024 * 1024 * 1024)).toFixed(2)} GiB`
}
