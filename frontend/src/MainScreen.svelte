<script lang="ts">
  import logo from "./assets/images/logo-universal.png";
  import backgroundMusic from "./assets/background-music.mp3";
  import {
    ActiveDownload,
    DownloadPercent,
    DownloadedBytes,
    DownloadedFiles,
    LocalEnabled,
    MusicEnabled,
    NeedsUpdate,
    Play,
    PlayDirect,
    Revision,
    ToggleMusic,
    TotalBytes,
    TotalFiles,
    Update,
    Version,
  } from "../wailsjs/go/main/App.js";
  import { onMount } from "svelte";
  import PlayIcon from "./PlayIcon.svelte";
  import UpdateIcon from "./UpdateIcon.svelte";
  import DownloadIcon from "./DownloadIcon.svelte";
  import SettingsIcon from "./SettingsIcon.svelte";
  import MusicIcon from "./MusicIcon.svelte";

  export let openSettings: () => void;

  let version: string = "";
  let revision: number = 0;
  let updating: boolean = false;
  let ready: boolean = false;
  let needsUpdate: boolean = false;

  let progress: number = 0;
  let totalFiles: number = 0;
  let totalBytes: number = 0;
  let downloadedFiles: number = 0;
  let downloadedBytes: number = 0;
  let activeDownload: string = "";

  let hasLocal = false;
  let checking = false;
  let musicEnabled = true;
  let audioElement: HTMLAudioElement;

  onMount(async () => {
    revision = await Revision();
    version = await Version();
    console.log("Checking if needs update...");
    needsUpdate = await NeedsUpdate();
    console.log("NeedsUpdate result:", needsUpdate);
    ready = true;
    hasLocal = await LocalEnabled();
    musicEnabled = await MusicEnabled();
    
    // Setup audio
    audioElement = new Audio(backgroundMusic);
    audioElement.loop = true;
    audioElement.volume = 0.3;
    
    if (musicEnabled) {
      audioElement.play().catch(err => console.log("Audio autoplay blocked:", err));
    }
    
    // Auto-start update if needed
    if (needsUpdate) {
      console.log("Update needed! Starting automatic download...");
      setTimeout(() => {
        update(); // Auto-start update
      }, 1000); // Wait 1 second to show UI first
    }
  });

  async function checkForUpdates() {
    checking = true;
    console.log("Manual check started...");
    needsUpdate = await NeedsUpdate();
    console.log("Manual check result:", needsUpdate);
    checking = false;
  }

  function update() {
    totalFiles = 0;
    totalBytes = 0;
    downloadedBytes = 0;
    downloadedFiles = 0;
    void Update();
    updating = true;

    const interval = setInterval(async () => {
      totalFiles = await TotalFiles();
      totalBytes = await TotalBytes();
      downloadedBytes = await DownloadedBytes();
      downloadedFiles = await DownloadedFiles();
      activeDownload = await ActiveDownload();
      progress = await DownloadPercent();

      if (downloadedFiles === totalFiles) {
        updating = false;
        needsUpdate = false;
        clearInterval(interval);
      }
    }, 1000);
  }

  async function toggleMusic() {
    musicEnabled = !musicEnabled;
    await ToggleMusic(musicEnabled);
    
    if (musicEnabled) {
      audioElement.play().catch(err => console.log("Error playing audio:", err));
    } else {
      audioElement.pause();
    }
  }

  function formatBytes(bytes: number, decimals = 2) {
    if (!+bytes) return "0 Bytes";

    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ["Bytes", "KiB", "MiB", "GiB", "TiB"];

    const i = Math.floor(Math.log(bytes) / Math.log(k));

    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
  }

  async function play() {
    ready = false;
    console.log("Launching OTClient...");
    try {
      await PlayDirect();
      console.log("OTClient launched!");
    } catch (e) {
      console.error("Error launching:", e);
    }
    // Re-enable after 3 seconds
    setTimeout(() => {
      ready = true;
    }, 3000);
  }

  async function playLocal() {
    ready = false;
    await Play(true);
    setTimeout(() => {
      ready = true;
    }, 3000);
  }
</script>

<div class="main-container">
  <button class="settings-btn" on:click={openSettings} disabled={updating}>
    <SettingsIcon />
  </button>
  
  <button class="music-btn" on:click={toggleMusic} title={musicEnabled ? "Desligar Música" : "Ligar Música"}>
    <MusicIcon playing={musicEnabled} />
  </button>
  
  <div class="launcher-panel">
    <!-- Logo apenas -->
    <div class="logo-section">
      <img alt="Nordemon Logo" class="main-logo" src={logo} />
    </div>

    <!-- Botões Principais -->
    <div class="actions">
      <!-- Verificar Atualizações -->
      <button 
        class="action-btn verify-btn" 
        on:click={checkForUpdates} 
        disabled={!ready || updating || checking}
      >
        <UpdateIcon />
        <span>{checking ? "Verificando..." : "Verificar Atualizações"}</span>
      </button>

      <!-- Atualizar Cliente -->
      {#if needsUpdate && !updating}
        <button 
          class="action-btn update-btn" 
          on:click={update} 
          disabled={!ready}
        >
          <DownloadIcon />
          <span>Atualizar Cliente</span>
        </button>
      {/if}

      <!-- Jogar -->
      <button 
        class="action-btn play-btn" 
        disabled={!ready || needsUpdate || updating}
        on:click={play}
      >
        <PlayIcon />
        <span>Jogar</span>
      </button>
    </div>

    <!-- Barra de Progresso (durante atualização) -->
    {#if updating}
      <div class="progress-section">
        <div class="progress-bar">
          <div class="progress-fill" style="width: {progress}%" />
        </div>
        <div class="progress-info">
          <span>{downloadedFiles} / {totalFiles} arquivos</span>
          <span>{formatBytes(downloadedBytes)} / {formatBytes(totalBytes)}</span>
        </div>
      </div>
    {/if}

    <!-- Status do Cliente -->
    {#if !updating && ready && !needsUpdate}
      <div class="status-ok">
        ✓ Cliente atualizado e pronto para jogar!
      </div>
    {/if}

    <!-- Rodapé - Status do Servidor -->
    <div class="server-status">
      <div class="status-indicator">
        <span class="globe-icon">🌐</span>
        <span class="status-text">Servidor Online</span>
      </div>
      <div class="latency">Latência: 23 ms</div>
    </div>
  </div>
</div>

<style>
  .main-container {
    width: 100%;
    height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    position: relative;
    background-image: url('./assets/images/pokemon-background.jpg');
    background-size: cover;
    background-position: center;
    background-repeat: no-repeat;
  }

  .main-container::before {
    content: '';
    position: absolute;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 0;
  }

  .settings-btn {
    position: absolute;
    top: 20px;
    right: 20px;
    width: 48px;
    height: 48px;
    background: rgba(26, 26, 46, 0.7);
    backdrop-filter: blur(8px);
    border: 2px solid rgba(59, 76, 202, 0.4);
    border-radius: 12px;
    cursor: pointer;
    transition: all 0.3s ease;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 10;
  }

  .settings-btn:hover:not(:disabled) {
    background: rgba(26, 26, 46, 0.9);
    border-color: rgba(59, 76, 202, 0.8);
    transform: scale(1.05);
  }

  .settings-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .music-btn {
    position: absolute;
    top: 20px;
    right: 80px;
    width: 48px;
    height: 48px;
    background: rgba(26, 26, 46, 0.7);
    backdrop-filter: blur(8px);
    border: 2px solid rgba(59, 76, 202, 0.4);
    border-radius: 12px;
    cursor: pointer;
    transition: all 0.3s ease;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 10;
    color: #3B4CCA;
  }

  .music-btn:hover {
    background: rgba(26, 26, 46, 0.9);
    border-color: rgba(59, 76, 202, 0.8);
    transform: scale(1.05);
    color: #5E72E4;
  }

  .launcher-panel {
    position: relative;
    z-index: 1;
    background: rgba(15, 15, 30, 0.85);
    backdrop-filter: blur(16px);
    border-radius: 24px;
    padding: 32px 28px 20px;
    width: 340px;
    min-height: 580px;
    display: flex;
    flex-direction: column;
    box-shadow: 
      0 8px 32px rgba(0, 0, 0, 0.7),
      0 0 0 1px rgba(59, 76, 202, 0.3),
      inset 0 1px 0 rgba(255, 255, 255, 0.05);
  }

  /* Logo */
  .logo-section {
    text-align: center;
    margin-bottom: 32px;
    padding-top: 8px;
  }

  .main-logo {
    width: 180px;
    height: 180px;
    margin: 0;
    filter: drop-shadow(0 0 20px rgba(59, 76, 202, 0.8));
    animation: glow 3s ease-in-out infinite;
  }

  @keyframes glow {
    0%, 100% { filter: drop-shadow(0 0 20px rgba(59, 76, 202, 0.6)); }
    50% { filter: drop-shadow(0 0 30px rgba(59, 76, 202, 1)); }
  }

  /* Botões de Ação */
  .actions {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-bottom: 24px;
    flex: 1;
  }

  .action-btn {
    width: 100%;
    padding: 18px;
    border: 2px solid;
    border-radius: 14px;
    font-size: 17px;
    font-weight: 700;
    cursor: pointer;
    transition: all 0.3s ease;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 12px;
    position: relative;
    overflow: hidden;
    text-transform: none;
  }

  .action-btn::before {
    content: '';
    position: absolute;
    inset: 0;
    background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.15), transparent);
    transform: translateX(-100%);
    transition: transform 0.6s;
  }

  .action-btn:hover::before {
    transform: translateX(100%);
  }

  .verify-btn {
    background: linear-gradient(135deg, #3B4CCA 0%, #5E72E4 100%);
    border-color: rgba(94, 114, 228, 0.5);
    color: white;
    box-shadow: 0 4px 16px rgba(59, 76, 202, 0.4);
  }

  .verify-btn:hover:not(:disabled) {
    background: linear-gradient(135deg, #4B5CDA 0%, #6E82F4 100%);
    border-color: rgba(94, 114, 228, 0.8);
    box-shadow: 0 6px 20px rgba(59, 76, 202, 0.6);
    transform: translateY(-2px);
  }

  .play-btn {
    background: linear-gradient(135deg, #6A4CCA 0%, #8A5CDA 100%);
    border-color: rgba(138, 92, 218, 0.5);
    color: white;
    font-size: 18px;
    padding: 18px;
    box-shadow: 0 4px 16px rgba(106, 76, 202, 0.5);
  }

  .play-btn:hover:not(:disabled) {
    background: linear-gradient(135deg, #7A5CDA 0%, #9A6CEA 100%);
    border-color: rgba(138, 92, 218, 0.8);
    box-shadow: 0 6px 24px rgba(106, 76, 202, 0.7);
    transform: translateY(-2px);
  }

  .action-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    transform: none !important;
  }

  /* Progresso */
  .progress-section {
    margin: 20px 0;
  }

  .progress-bar {
    width: 100%;
    height: 8px;
    background: rgba(30, 30, 50, 0.8);
    border-radius: 8px;
    overflow: hidden;
    margin-bottom: 8px;
  }

  .progress-fill {
    height: 100%;
    background: linear-gradient(90deg, #3B4CCA 0%, #FFD700 100%);
    transition: width 0.5s ease;
  }

  .progress-info {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
    color: #b8b8d1;
  }

  /* Status OK */
  .status-ok {
    padding: 14px;
    background: rgba(46, 204, 113, 0.15);
    border: 1px solid rgba(46, 204, 113, 0.4);
    border-radius: 10px;
    color: #2ecc71;
    font-size: 13px;
    font-weight: 600;
    text-align: center;
    margin-bottom: 24px;
  }

  /* Servidor Status */
  .server-status {
    padding-top: 18px;
    border-top: 1px solid rgba(94, 114, 228, 0.2);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    margin-top: auto;
  }

  .status-indicator {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 14px;
    font-weight: 600;
    color: #b8b8d1;
  }

  .globe-icon {
    font-size: 16px;
  }

  .status-text {
    color: #2ecc71;
  }

  .latency {
    font-size: 12px;
    color: #858595;
  }
</style>
