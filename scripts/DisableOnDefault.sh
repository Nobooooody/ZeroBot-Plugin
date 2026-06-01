find plugin -type f -exec sed -i -E 's/DisableOnDefault[[:space:]]*:[[:space:]]*false[[:space:]]*,/DisableOnDefault: true,/g' {} +
