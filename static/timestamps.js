function parseTimestamp(ts) {
  const parts = ts.split(":").map(Number).reverse();
  let seconds = 0;
  if (parts[0]) seconds += parts[0];          // seconds
  if (parts[1]) seconds += parts[1] * 60;     // minutes
  if (parts[2]) seconds += parts[2] * 3600;   // hours
  return seconds;
}

function makeTimestampsClickable(containerId, videoId, imageId) {
  const container = document.getElementById(containerId);
  const video = document.getElementById(videoId);
  const image = document.getElementById(imageId);

  // Regex for timestamps: [h:mm:ss] or [mm:ss] or [ss]
  const timestampRegex = /\[(\d{1,2}(?::\d{2}){0,2})\]/g;
  // Regex for rotations: [rotate90], [rotate180], [rotate270], [rotate0]
  const rotateRegex = /\[rotate(0|90|180|270)\]/g;

  // Replace timestamps
  container.innerHTML = container.innerHTML.replace(timestampRegex, (match, ts) => {
    const seconds = parseTimestamp(ts);
    return `<a href="#" class="timestamp" data-time="${seconds}">${match}</a>`;
  });

  // Replace rotations
  container.innerHTML = container.innerHTML.replace(rotateRegex, (match, angle) => {
    return `<a href="#" class="rotate" data-angle="${angle}">${match}</a>`;
  });

  // Handle clicks
  container.addEventListener("click", e => {
    if (e.target.classList.contains("timestamp")) {
      e.preventDefault();
      const time = Number(e.target.dataset.time);
      if (video) {
        video.currentTime = time;
        video.play();
      }
    } else if (e.target.classList.contains("rotate")) {
      e.preventDefault();
      const angle = Number(e.target.dataset.angle);
      // Apply rotation to whichever element exists
      const target = video || image;
      if (target) {
        target.style.transform = `rotate(${angle}deg)`;
        target.style.transformOrigin = "center center";
      }
    }
  });
}

// Run it
document.addEventListener("DOMContentLoaded", () => {
  makeTimestampsClickable("current-description", "videoPlayer", "imageViewer");
});