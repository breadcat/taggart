function parseTimestamp(ts) {
  // Split by ":" and reverse to handle h:m:s flexibly
  const parts = ts.split(":").map(Number).reverse();
  let seconds = 0;
  if (parts[0]) seconds += parts[0];          // seconds
  if (parts[1]) seconds += parts[1] * 60;     // minutes
  if (parts[2]) seconds += parts[2] * 3600;   // hours
  return seconds;
}

function makeTimestampsClickable(containerId, videoId) {
  const container = document.getElementById(containerId);
  const video = document.getElementById(videoId);

  // Regex: [h:mm:ss] or [mm:ss] or [ss]
  const regex = /\[(\d{1,2}(?::\d{2}){0,2})\]/g;

  container.innerHTML = container.innerHTML.replace(regex, (match, ts) => {
    const seconds = parseTimestamp(ts);
    return `<a href="#" class="timestamp" data-time="${seconds}">${match}</a>`;
  });

  container.addEventListener("click", e => {
    if (e.target.classList.contains("timestamp")) {
      e.preventDefault();
      const time = Number(e.target.dataset.time);
      video.currentTime = time;
      video.play();
    }
  });
}

// Run it
document.addEventListener("DOMContentLoaded", () => {
  makeTimestampsClickable("current-description", "videoPlayer");
});